//go:build windows && desktop

package windows

import (
	"github.com/wailsapp/wails/v2/pkg/options"
	wo "github.com/wailsapp/wails/v2/pkg/options/windows"
	"os"
	"path/filepath"
)

func ConfigureDesktop(app *options.App, data, exeDir string) {
	fixed := ""
	for _, p := range []string{filepath.Join(exeDir, "runtime", "webview2"), filepath.Join(exeDir, "webview2")} {
		if _, e := os.Stat(filepath.Join(p, "msedgewebview2.exe")); e == nil {
			fixed = p
			break
		}
	}
	app.Windows = &wo.Options{WebviewUserDataPath: filepath.Join(data, "desktop-cache"), WebviewBrowserPath: fixed, Theme: wo.SystemDefault, Messages: &wo.Messages{InstallationRequired: "桌面界面需要 Microsoft WebView2，当前电脑尚未安装。", UpdateRequired: "当前 WebView2 版本太旧。", MissingRequirements: "需要桌面运行组件", Webview2NotInstalled: "未安装 WebView2。也可以将微软官方固定版运行时放入程序旁的 runtime\\webview2 文件夹。", Error: "启动失败", FailedToInstall: "WebView2 安装未完成，请检查网络后重试。", DownloadPage: "请从微软官网下载 WebView2。", PressOKToInstall: "\n点击确定将使用内置引导程序联网安装，取消则退出。", ContactAdmin: "请联系设备管理员。", InvalidFixedWebview2: "随程序提供的 WebView2 运行时无效。", WebView2ProcessCrash: "桌面界面异常退出，请重新启动管理器。"}}
}
