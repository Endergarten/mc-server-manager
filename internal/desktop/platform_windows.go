//go:build windows && desktop

package desktop

import (
	"github.com/Endergarten/mc-server-manager/internal/platform/windows"
	"github.com/wailsapp/wails/v2/pkg/options"
)

func configurePlatform(app *options.App, data, exeDir string) {
	windows.ConfigureDesktop(app, data, exeDir)
}
func Origin() string { return "http://wails.localhost" }
