//go:build windows

package harness

import (
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

const createNoWindow = 0x08000000

func configureCmd(cmd *exec.Cmd, bin string, args []string) {
	ext := strings.ToLower(filepath.Ext(bin))
	if ext == ".cmd" || ext == ".bat" {
		wrapped := make([]string, 0, len(args)+2)
		wrapped = append(wrapped, "/c", bin)
		wrapped = append(wrapped, args...)
		cmd.Path = ""
		if p, err := exec.LookPath("cmd.exe"); err == nil {
			cmd.Path = p
		} else {
			cmd.Path = "cmd.exe"
		}
		cmd.Args = append([]string{cmd.Path}, wrapped...)
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: windows.CREATE_NEW_PROCESS_GROUP | createNoWindow,
	}
}

func attachProcessTree(cmd *exec.Cmd) (func() error, error) {
	if cmd.Process == nil {
		return func() error { return nil }, nil
	}
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return nil, err
	}
	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err := windows.SetInformationJobObject(
		job,
		windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)),
		uint32(unsafe.Sizeof(info)),
	); err != nil {
		_ = windows.CloseHandle(job)
		return nil, err
	}

	procHandle, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE|windows.SYNCHRONIZE, false, uint32(cmd.Process.Pid))
	if err != nil {
		_ = windows.CloseHandle(job)
		return nil, err
	}
	assignErr := windows.AssignProcessToJobObject(job, procHandle)
	_ = windows.CloseHandle(procHandle)
	if assignErr != nil {
		_ = windows.CloseHandle(job)
		return nil, assignErr
	}

	var once sync.Once
	var termErr error
	return func() error {
		once.Do(func() {
			termErr = windows.TerminateJobObject(job, 1)
			_ = windows.CloseHandle(job)
		})
		return termErr
	}, nil
}
