//go:build windows

package setup

import (
	"os/exec"
	"syscall"
)

const createNoWindow = 0x08000000

func configureCmd(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: createNoWindow,
	}
}
