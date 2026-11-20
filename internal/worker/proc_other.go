//go:build !windows

package worker

import "os/exec"

func configureCmd(_ *exec.Cmd) {}
