//go:build windows

package terminal

import (
	"os"
	"strings"
	"sync"
	"unicode/utf16"
	"unsafe"

	"golang.org/x/sys/windows"
)

type windowsPTY struct {
	hpc      windows.Handle
	inWrite  *os.File
	outRead  *os.File
	procInfo windows.ProcessInformation
	attrList *windows.ProcThreadAttributeListContainer

	waitOnce sync.Once
	exitCode int
	waitErr  error
	closed   sync.Once
}

func defaultShell(override string) string {
	if strings.TrimSpace(override) != "" {
		return override
	}
	if sh := strings.TrimSpace(os.Getenv("COMSPEC")); sh != "" {
		return sh
	}
	return "cmd.exe"
}

func buildUTF16EnvBlock(env []string) *uint16 {
	var buf []uint16
	for _, entry := range env {
		buf = append(buf, utf16.Encode([]rune(entry))...)
		buf = append(buf, 0)
	}
	buf = append(buf, 0)
	return &buf[0]
}

func startPTY(opts StartOptions) (PTY, error) {
	cols, rows := normalizeSize(opts.Cols, opts.Rows)

	var inRead, inWrite, outRead, outWrite windows.Handle
	if err := windows.CreatePipe(&inRead, &inWrite, nil, 0); err != nil {
		return nil, err
	}
	if err := windows.CreatePipe(&outRead, &outWrite, nil, 0); err != nil {
		_ = windows.CloseHandle(inRead)
		_ = windows.CloseHandle(inWrite)
		return nil, err
	}

	var hpc windows.Handle
	err := windows.CreatePseudoConsole(windows.Coord{X: int16(cols), Y: int16(rows)}, inRead, outWrite, 0, &hpc)
	_ = windows.CloseHandle(inRead)
	_ = windows.CloseHandle(outWrite)
	if err != nil {
		_ = windows.CloseHandle(inWrite)
		_ = windows.CloseHandle(outRead)
		return nil, err
	}

	attrList, err := windows.NewProcThreadAttributeList(1)
	if err != nil {
		windows.ClosePseudoConsole(hpc)
		_ = windows.CloseHandle(inWrite)
		_ = windows.CloseHandle(outRead)
		return nil, err
	}
	if err := attrList.Update(windows.PROC_THREAD_ATTRIBUTE_PSEUDOCONSOLE, unsafe.Pointer(hpc), unsafe.Sizeof(hpc)); err != nil {
		attrList.Delete()
		windows.ClosePseudoConsole(hpc)
		_ = windows.CloseHandle(inWrite)
		_ = windows.CloseHandle(outRead)
		return nil, err
	}

	var si windows.StartupInfoEx
	si.Cb = uint32(unsafe.Sizeof(si))
	si.ProcThreadAttributeList = attrList.List()

	sh := defaultShell(opts.Shell)
	line := sh
	if opts.Command != "" {
		line = sh + " /C " + opts.Command
	}
	cmdLine, err := windows.UTF16PtrFromString(line)
	if err != nil {
		attrList.Delete()
		windows.ClosePseudoConsole(hpc)
		_ = windows.CloseHandle(inWrite)
		_ = windows.CloseHandle(outRead)
		return nil, err
	}
	var dirPtr *uint16
	if opts.Directory != "" {
		dirPtr, _ = windows.UTF16PtrFromString(opts.Directory)
	}
	env := os.Environ()
	if len(opts.Env) > 0 {
		env = opts.Env
	}
	env = append(env, "TERM=xterm-256color", "COLORTERM=truecolor")
	envBlock := buildUTF16EnvBlock(env)

	var pi windows.ProcessInformation
	err = windows.CreateProcess(
		nil,
		cmdLine,
		nil,
		nil,
		false,
		windows.EXTENDED_STARTUPINFO_PRESENT|windows.CREATE_UNICODE_ENVIRONMENT,
		envBlock,
		dirPtr,
		&si.StartupInfo,
		&pi,
	)
	if err != nil {
		attrList.Delete()
		windows.ClosePseudoConsole(hpc)
		_ = windows.CloseHandle(inWrite)
		_ = windows.CloseHandle(outRead)
		return nil, err
	}

	return &windowsPTY{
		hpc:      hpc,
		inWrite:  os.NewFile(uintptr(inWrite), "conpty-in"),
		outRead:  os.NewFile(uintptr(outRead), "conpty-out"),
		procInfo: pi,
		attrList: attrList,
	}, nil
}

func (p *windowsPTY) Read(b []byte) (int, error)  { return p.outRead.Read(b) }
func (p *windowsPTY) Write(b []byte) (int, error) { return p.inWrite.Write(b) }

func (p *windowsPTY) Resize(cols, rows int) error {
	c, r := normalizeSize(cols, rows)
	return windows.ResizePseudoConsole(p.hpc, windows.Coord{X: int16(c), Y: int16(r)})
}

func (p *windowsPTY) PID() int { return int(p.procInfo.ProcessId) }

func (p *windowsPTY) Wait() (int, error) {
	p.waitOnce.Do(func() {
		_, p.waitErr = windows.WaitForSingleObject(p.procInfo.Process, windows.INFINITE)
		var code uint32
		if err := windows.GetExitCodeProcess(p.procInfo.Process, &code); err == nil {
			p.exitCode = int(code)
		}
	})
	return p.exitCode, p.waitErr
}

func (p *windowsPTY) Close() error {
	p.closed.Do(func() {
		_ = windows.TerminateProcess(p.procInfo.Process, 1)
		windows.ClosePseudoConsole(p.hpc)
		_ = p.inWrite.Close()
		_ = p.outRead.Close()
		_, _ = p.Wait()
		_ = windows.CloseHandle(p.procInfo.Thread)
		_ = windows.CloseHandle(p.procInfo.Process)
		p.attrList.Delete()
	})
	return nil
}
