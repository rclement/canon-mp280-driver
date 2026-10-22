// SPDX-License-Identifier: GPL-2.0-only

//go:build !linux

package cupsbackend

import "os/exec"

func setPdeathsig(cmd *exec.Cmd) {}
