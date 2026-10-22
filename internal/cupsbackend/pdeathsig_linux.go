// SPDX-License-Identifier: GPL-2.0-only

//go:build linux

package cupsbackend

import (
	"os/exec"
	"syscall"
)

func setPdeathsig(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Pdeathsig = syscall.SIGTERM
}
