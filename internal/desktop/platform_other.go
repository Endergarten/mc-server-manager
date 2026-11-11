//go:build !windows && desktop

package desktop

import "github.com/wailsapp/wails/v2/pkg/options"

// The portable host is kept buildable; distribution/testing is currently Windows-only.
func configurePlatform(app *options.App, data, exeDir string) {}
func Origin() string                                          { return "wails://wails" }
