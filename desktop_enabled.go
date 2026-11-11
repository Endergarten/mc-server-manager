//go:build desktop

package main

import (
	"fmt"
	"github.com/Endergarten/mc-server-manager/internal/desktop"
	"os"
	"path/filepath"
)

const desktopAvailable = true

func runDesktop(a *App) error {
	exe, _ := os.Executable()
	a.desktop = true
	a.address = desktop.Origin()
	return desktop.Run(desktop.Config{Handler: a.guard(a.handler()), DataPath: a.root, ExeDir: filepath.Dir(exe), Stop: a.stopAndWait, Running: func() bool {
		a.mu.Lock()
		defer a.mu.Unlock()
		return a.run != nil && a.run.State != "stopped" && a.run.State != "failed"
	}, Closing: func(v bool) { a.mu.Lock(); a.closing = v; a.mu.Unlock() }, Ready: func(pick func(string) (string, error), quit func()) {
		a.mu.Lock()
		a.pickNative = pick
		a.shutdown = quit
		a.mu.Unlock()
	}, Drop: func(paths []string) (any, error) {
		if len(paths) != 1 {
			return nil, fmt.Errorf("请每次拖入一个地图 ZIP")
		}
		return a.inspectMapPath(paths[0])
	}})
}
