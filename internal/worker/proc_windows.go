//go:build windows

package worker

import (
	"os/exec"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

const createNoWindow = 0x08000000

type procHandle struct {
	mu  sync.Mutex
	cmd *exec.Cmd
	job windows.Handle
}

func configureCmd(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: createNoWindow,
	}
}

func attachProcess(cmd *exec.Cmd) *procHandle {
	h := &procHandle{cmd: cmd}
	if cmd == nil || cmd.Process == nil {
		return h
	}
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return h
	}
	var info windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION
	info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err := windows.SetInformationJobObject(
		job,
		windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)),
		uint32(unsafe.Sizeof(info)),
	); err != nil {
		_ = windows.CloseHandle(job)
		return h
	}
	proc, err := windows.OpenProcess(
		windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE,
		false,
		uint32(cmd.Process.Pid),
	)
	if err != nil {
		_ = windows.CloseHandle(job)
		return h
	}
	defer windows.CloseHandle(proc)

	if err := windows.AssignProcessToJobObject(job, proc); err != nil {
		_ = windows.CloseHandle(job)
		return h
	}
	h.job = job
	return h
}

func (h *procHandle) kill() {
	if h == nil {
		return
	}
	h.mu.Lock()
	job := h.job
	h.job = 0
	cmd := h.cmd
	h.mu.Unlock()

	if job != 0 {
		_ = windows.TerminateJobObject(job, 1)
		_ = windows.CloseHandle(job)
	}
	if cmd != nil && cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
}

func (h *procHandle) close() {
	if h == nil {
		return
	}
	h.mu.Lock()
	job := h.job
	h.job = 0
	h.mu.Unlock()

	if job != 0 {
		_ = windows.CloseHandle(job)
	}
}
