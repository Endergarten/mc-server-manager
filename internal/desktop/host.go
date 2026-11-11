//go:build desktop

// Package desktop hosts the shared UI. Business logic stays outside this package.
package desktop

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	wr "github.com/wailsapp/wails/v2/pkg/runtime"
)

type Config struct {
	Handler  http.Handler
	DataPath string
	ExeDir   string
	Stop     func(time.Duration) error
	Running  func() bool
	Closing  func(bool)
	Ready    func(pick func(string) (string, error), quit func())
	Drop     func([]string) (any, error)
}
type host struct {
	cfg                 Config
	ctx                 context.Context
	mu                  sync.Mutex
	exiting, allowClose bool
}

func Run(cfg Config) error {
	h := &host{cfg: cfg}
	app := &options.App{Title: "MC Server Manager", Width: 1280, Height: 850, MinWidth: 900, MinHeight: 650, BackgroundColour: options.NewRGB(245, 246, 243), AssetServer: &assetserver.Options{Handler: cfg.Handler}, OnStartup: h.startup, OnBeforeClose: h.beforeClose, EnableDefaultContextMenu: true, DragAndDrop: &options.DragAndDrop{EnableFileDrop: true, DisableWebViewDrop: true}, SingleInstanceLock: &options.SingleInstanceLock{UniqueId: "6e22b376-2cc0-4ad6-a065-fbdcce061035", OnSecondInstanceLaunch: func(options.SecondInstanceData) {
		if h.ctx != nil {
			wr.WindowShow(h.ctx)
			wr.WindowUnminimise(h.ctx)
		}
	}}}
	configurePlatform(app, cfg.DataPath, cfg.ExeDir)
	return wails.Run(app)
}
func (h *host) startup(ctx context.Context) {
	h.ctx = ctx
	h.cfg.Ready(h.pick, func() { h.requestQuit() })
	wr.EventsOnce(ctx, "manager:ready", func(...interface{}) { log.Print("Desktop interface ready") })
	wr.OnFileDrop(ctx, func(x, y int, paths []string) {
		go func() {
			wr.EventsEmit(ctx, "manager:drop-progress")
			result, e := h.cfg.Drop(paths)
			if e != nil {
				wr.EventsEmit(ctx, "manager:drop", map[string]any{"error": e.Error()})
				return
			}
			wr.EventsEmit(ctx, "manager:drop", map[string]any{"stage": result})
		}()
	})
}
func (h *host) pick(kind string) (string, error) {
	switch kind {
	case "java":
		return wr.OpenFileDialog(h.ctx, wr.OpenDialogOptions{Title: "选择 Java 程序", Filters: []wr.FileFilter{{DisplayName: "Java 程序 (java.exe)", Pattern: "java.exe"}}})
	case "directory":
		return wr.OpenDirectoryDialog(h.ctx, wr.OpenDialogOptions{Title: "选择 Java 搜索文件夹"})
	default:
		return "", fmt.Errorf("不支持的文件选择类型")
	}
}
func (h *host) beforeClose(ctx context.Context) bool {
	h.mu.Lock()
	allowed := h.allowClose
	h.mu.Unlock()
	if allowed {
		return false
	}
	h.requestQuit()
	return true
}
func (h *host) requestQuit() {
	h.mu.Lock()
	if h.exiting {
		h.mu.Unlock()
		return
	}
	h.exiting = true
	h.mu.Unlock()
	go func() {
		if h.cfg.Running() {
			answer, e := wr.MessageDialog(h.ctx, wr.MessageDialogOptions{Type: wr.QuestionDialog, Title: "退出 MC Server Manager", Message: "服务器仍在运行。保存地图并停止后退出吗？", Buttons: []string{"Yes", "No"}, DefaultButton: "No", CancelButton: "No"})
			if e != nil || answer != "Yes" {
				h.mu.Lock()
				h.exiting = false
				h.mu.Unlock()
				return
			}
		}
		h.cfg.Closing(true)
		if e := h.cfg.Stop(90 * time.Second); e != nil {
			h.cfg.Closing(false)
			_, _ = wr.MessageDialog(h.ctx, wr.MessageDialogOptions{Type: wr.ErrorDialog, Title: "尚未退出", Message: e.Error() + "。程序保持打开，不会强制结束服务器。"})
			h.mu.Lock()
			h.exiting = false
			h.mu.Unlock()
			return
		}
		h.mu.Lock()
		h.allowClose = true
		h.mu.Unlock()
		wr.Quit(h.ctx)
	}()
}
