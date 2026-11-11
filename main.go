package main

import (
	"context"
	"crypto/rand"
	"embed"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

//go:embed web/*
var webFiles embed.FS

func main() {
	exe, _ := os.Executable()
	data := flag.String("data", filepath.Join(filepath.Dir(exe), "data"), "地图和配置目录")
	port := flag.Int("port", 18180, "本机管理页面端口")
	noBrowser := flag.Bool("no-browser", false, "不自动打开浏览器")
	flag.Parse()
	app, err := newApp(*data)
	if err != nil {
		log.Fatal(err)
	}
	listener, err := net.Listen("tcp4", fmt.Sprintf("127.0.0.1:%d", *port))
	if err != nil {
		log.Fatalf("管理器端口无法使用，可能已启动：%v", err)
	}
	app.address = "http://" + listener.Addr().String()
	b := make([]byte, 32)
	if _, err = rand.Read(b); err != nil {
		log.Fatal(err)
	}
	app.token = hex.EncodeToString(b)
	mux := http.NewServeMux()
	app.routes(mux)
	static, _ := fs.Sub(webFiles, "web")
	mux.Handle("/", http.FileServer(http.FS(static)))
	server := &http.Server{Handler: app.guard(mux), ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 60 * time.Second}
	app.shutdown = func() {
		app.mu.Lock()
		if app.closing {
			app.mu.Unlock()
			return
		}
		app.closing = true
		app.mu.Unlock()
		go func() {
			if err := app.stopAndWait(60 * time.Second); err != nil {
				app.addLog("无法退出：" + err.Error())
				app.mu.Lock()
				app.closing = false
				app.mu.Unlock()
				return
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = server.Shutdown(ctx)
		}()
	}
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt)
	go func() { <-signals; app.shutdown() }()
	fmt.Printf("\nMC Server Manager\n管理页面：%s\n数据目录：%s\n请在网页点击“退出管理器”，以便正常保存并关闭服务器。\n", app.address, app.root)
	if !*noBrowser {
		openBrowser(app.address)
	}
	if err := server.Serve(listener); err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
}

func openBrowser(address string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", address)
	case "darwin":
		cmd = exec.Command("open", address)
	default:
		cmd = exec.Command("xdg-open", address)
	}
	_ = cmd.Start()
}

func (a *App) guard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		expected, _ := url.Parse(a.address)
		if r.Host != expected.Host {
			http.Error(w, "仅允许本机管理地址", 403)
			return
		}
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; img-src 'self' blob:; style-src 'self'; script-src 'self'; connect-src 'self'; frame-ancestors 'none'")
		if strings.HasPrefix(r.URL.Path, "/api/") {
			w.Header().Set("Cache-Control", "no-store")
			if origin := r.Header.Get("Origin"); origin != "" && origin != a.address {
				http.Error(w, "不允许跨站请求", 403)
				return
			}
			if r.Method != "GET" && r.Header.Get("X-Manager-Token") != a.token {
				http.Error(w, "请刷新管理页面重试", 403)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func respond(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(v)
}
func fail(w http.ResponseWriter, err error) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(400)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
}
func decode(w http.ResponseWriter, r *http.Request, v any) error {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	return d.Decode(v)
}
