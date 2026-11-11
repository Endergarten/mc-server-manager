package main

import (
	winplatform "github.com/Endergarten/mc-server-manager/internal/platform/windows"
	"os/exec"
)

func hideWindow(cmd *exec.Cmd) { winplatform.HideProcess(cmd) }
