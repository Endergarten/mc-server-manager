//go:build !desktop

package main

import "fmt"

const desktopAvailable = false

func runDesktop(a *App) error {
	return fmt.Errorf("当前是网页构建，请使用 Windows 桌面发行版")
}
