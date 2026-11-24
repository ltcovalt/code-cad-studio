//go:build !windows && !linux

package worker

import (
	"os/exec"
	"syscall"
)

type procHandle struct {
	cmd *exec.Cmd
}

func configureCmd(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setpgid: true,
	}
}

func attachProcess(cmd *exec.Cmd) *procHandle {
	return &procHandle{cmd: cmd}
}

func (h *procHandle) kill() {
	if h == nil || h.cmd == nil || h.cmd.Process == nil {
		return
	}
	pid := h.cmd.Process.Pid
	if pid > 0 {
		if err := syscall.Kill(-pid, syscall.SIGKILL); err == nil {
			return
		}
	}
	_ = h.cmd.Process.Kill()
}

func (h *procHandle) close() {}
