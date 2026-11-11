//go:build windows

package windows

import (
	"os/exec"
	"syscall"
)

func HideProcess(cmd *exec.Cmd) { cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true} }
