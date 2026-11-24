//go:build !windows

package setup

import "os/exec"

func configureCmd(_ *exec.Cmd) {}
