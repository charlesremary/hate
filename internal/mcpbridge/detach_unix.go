// Copyright 2026 Charles Emary
// SPDX-License-Identifier: FSL-1.1-Apache-2.0

//go:build !windows

package mcpbridge

import (
	"os/exec"
	"syscall"
)

// detach starts the app in its own session so it outlives the bridge.
func detach(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}
