//go:build windows

package opencode

import (
	"os/exec"
	"sync"
	"syscall"
	"unsafe"
)

// startOwned starts cmd and puts it in a job object that kills its
// processes when the job's last handle closes. The app holds that handle
// until it exits, so the server dies with it, also when it crashes. Owning
// it is best effort: the server runs even if Windows refuses the job.
func startOwned(cmd *exec.Cmd) error {
	if err := cmd.Start(); err != nil {
		return err
	}
	job, err := killOnCloseJob()
	if err != nil {
		return nil
	}
	h, err := syscall.OpenProcess(processSetQuota|processTerminate, false, uint32(cmd.Process.Pid))
	if err != nil {
		return nil
	}
	defer syscall.CloseHandle(h)
	assignProcessToJobObject.Call(uintptr(job), uintptr(h))
	return nil
}

const (
	processTerminate                  = 0x0001
	processSetQuota                   = 0x0100
	jobObjectExtendedLimitInformation = 9
	jobObjectLimitKillOnJobClose      = 0x2000
)

var (
	kernel32                 = syscall.NewLazyDLL("kernel32.dll")
	createJobObject          = kernel32.NewProc("CreateJobObjectW")
	setInformationJobObject  = kernel32.NewProc("SetInformationJobObject")
	assignProcessToJobObject = kernel32.NewProc("AssignProcessToJobObject")
)

// killOnCloseJob is the app's one job, made the first time a server starts
// and never closed.
var killOnCloseJob = sync.OnceValues(func() (syscall.Handle, error) {
	r, _, err := createJobObject.Call(0, 0)
	if r == 0 {
		return 0, err
	}
	job := syscall.Handle(r)
	var info jobExtendedLimit
	info.Basic.LimitFlags = jobObjectLimitKillOnJobClose
	if ok, _, err := setInformationJobObject.Call(uintptr(job), jobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)), unsafe.Sizeof(info)); ok == 0 {
		syscall.CloseHandle(job)
		return 0, err
	}
	return job, nil
})

// jobExtendedLimit is JOBOBJECT_EXTENDED_LIMIT_INFORMATION.
type jobExtendedLimit struct {
	Basic struct {
		PerProcessUserTimeLimit int64
		PerJobUserTimeLimit     int64
		LimitFlags              uint32
		MinimumWorkingSetSize   uintptr
		MaximumWorkingSetSize   uintptr
		ActiveProcessLimit      uint32
		Affinity                uintptr
		PriorityClass           uint32
		SchedulingClass         uint32
	}
	IO struct {
		ReadOperationCount, WriteOperationCount, OtherOperationCount uint64
		ReadTransferCount, WriteTransferCount, OtherTransferCount    uint64
	}
	ProcessMemoryLimit    uintptr
	JobMemoryLimit        uintptr
	PeakProcessMemoryUsed uintptr
	PeakJobMemoryUsed     uintptr
}
