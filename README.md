# MC Server Manager

面向 **Windows 10 / 11** 的 Minecraft 开服管理器，使用 Go + Wails。双击 EXE 直接进入独立桌面窗口，提供服务器列表、属性面板、底部控制台。界面按 Windows 工具软件重新设计。

当前支持原版 Java 服务端，同一时间运行一个实例，各实例独立保存地图和进度，共用服务端版本库。

## 使用

1. 解压 Windows 便携包，双击 `mc-server-manager.exe`，无需安装 Go、Python、Node.js 或 .NET。
2. 在“设置”中阅读并同意 [Minecraft EULA](https://www.minecraft.net/eula)，配置内存、游戏端口和管理员列表。
3. 点击“导入地图”，或将地图 ZIP 拖到服务器列表，确认实例名称和游戏版本。
4. 选中实例并启动；缺少服务端时，自动从 Mojang 下载、校验并启动。
5. 使用下方控制台发送命令，用“停止”正常保存关服。

本机游戏连接 `localhost:25565`，如修改游戏端口则使用相应端口。关闭桌面窗口或退出时，运行中的服务器会先询问是否保存停止；用户取消或保存超时则保留窗口，不会强杀。

## 功能

- 服务器列表：地图小图、名称、版本、状态，右侧显示简介、环境与开服配置。
- 地图 ZIP 导入：查找 `level.dat`，支持多层目录、多存档选择和手动确认版本。
- 优先读取 `mcversion.txt`，其次读取存档最后保存版本；自动读取 `icon.png`，可编辑简介和图片。
- 官方版本列表、准确区分正式版与测试版、下载进度、大小和 SHA-1 校验。
- 导入已有官方 JAR，识别内部版本；较旧版本官方下载使用官方摘要校验。
- 实时后台、命令输入、正常 stop 保存、停止状态备份。
- 可配置多个管理员，开服同步 OP，全新安装默认留空，无个人账号硬编码。
- 全局开服开关：正版验证（默认关闭）、命令方块（默认开启）、允许飞行（默认开启）。白名单固定关闭，不在界面中展示。
- 删除地图前确认，运行中的地图不可删除；完整地图与进度移入 `data/trash/`，原始 ZIP 和共享服务端保留。
- 自动搜索和匹配 Java，支持浏览本地文件与文件夹补充。

允许飞行不会自动赋予玩家飞行能力。离线模式不验证玩家身份，游戏连接仅应开放给信任的玩家。管理员配置开服时同步，原内容保存为 `ops.before-manager.json`；离线模式使用离线 UUID，正版验证开启时由官方服务端通过后台命令解析并授权，避免混用身份。运行中临时授权下次启动以配置列表为准。

## Java 自动管理

默认无需填写路径。搜索范围包括常见 Java 安装位置、Windows JavaSoft 注册信息、Minecraft / PCL / HMCL / Prism 等启动器常见运行时、`JAVA_HOME`、`PATH` 和补充目录。

这是有深度与时间限制的常见位置搜索，不会无限遍历整台电脑。如有遗漏，在“Java 环境”点击“浏览 java.exe…”或“扫描文件夹…”，添加后持久保存。

匹配顺序：用户兼容规则 → 已保存的 Mojang `javaVersion` → 官方服务端内嵌 `version.json` → 内置正式版兼容表。优先官方主版本及较新补丁版本，不会简单用最新版 Java 跑所有服务端。缺少兼容的 64 位 Java 时提示补充，不强行使用错误版本。

首次运行生成 `data/java-rules.json`，修改后下次开服读取。例如：

```json
{
  "overrides": [{"versions":["1.20.1"],"preferred":17,"allowed":[17]}],
  "fallbacks": [
    {"min":"26.1","preferred":25,"allowed":[25]},
    {"min":"1.20.5","before":"26.1","preferred":21,"allowed":[21]},
    {"min":"1.18","before":"1.20.5","preferred":17,"allowed":[17]},
    {"min":"1.17","before":"1.18","preferred":16,"allowed":[16]},
    {"min":"1.0","before":"1.17","preferred":8,"allowed":[8]}
  ]
}
```

`overrides` 明确覆盖官方要求，需自行确认兼容性。规则模板见 [config/java-rules.json](config/java-rules.json)。Minecraft 仍然需要 Java，当前不自动下载安装 Java。

## Windows 环境与离线包

桌面界面使用微软 WebView2，Windows 11 和大多数 Windows 10 已有该组件。程序内置微软安装引导，缺失时用户确认后联网安装。

若需离线且系统未安装 WebView2，可将微软官方 **Fixed Version Runtime** 完整解压到程序旁的 `runtime/webview2/`，其中应有 `msedgewebview2.exe` 及其他全部运行时文件。程序优先使用随附运行时。不能只复制一个 EXE。

普通便携包不包含完整 WebView2、Java 或 Minecraft JAR。构建脚本支持 `-WebViewRuntimeFolder <完整运行时目录>`，可将已有官方运行时一同打包。

参考：[Wails Windows 运行环境](https://v2.wails.io/docs/guides/windows/)、[微软 WebView2 分发说明](https://learn.microsoft.com/en-us/microsoft-edge/webview2/concepts/distribution)。

## 构建

开发需要 Go 1.25 或更高版本，无需 NPM、前端构建器或 Wails CLI：

```powershell
go test ./...
go vet ./...
go build -tags "desktop,production,wv2runtime.embed" -trimpath -ldflags "-H windowsgui" -o dist/mc-server-manager.exe .
```

可直接运行 `build.bat` 或 `scripts/windows/build.ps1`。GitHub Actions 构建 Windows x64 桌面包，在 Windows / Linux 验证共用逻辑；Linux 检查不代表发布 Linux 桌面应用。

保留网页兼容模式用于开发和自动测试：

```powershell
go run . --data ./data --port 18180 --no-browser
# 桌面构建可显式传 --web 使用兼容模式
```

数据默认在 EXE 旁的 `data/`，`go run` 请显式指定 `--data`。

## 模块

- `frontend/windows/`：全新 Windows 工具界面。
- `internal/desktop/`：独立桌面宿主、生命周期和文件浏览接口。
- `internal/platform/windows/`：Windows 窗口、WebView2、Java 安装信息和子进程适配。
- `internal/platform/darwin/`、`internal/platform/linux/`：其他平台接入位置，当前预留。
- `scripts/windows/`：Windows 构建和便携包脚本。

开服、导入、下载和 Java 匹配尽量使用跨平台代码。本次仅交付 Windows 10/11，macOS / Linux 桌面未适配与实机验证。

## 数据与限制

`data/` 包含实例说明、设置、Java 规则与搜索缓存、`servers/`、`instances/<id>/world/`、日志、`backups/`、`staging/` 和桌面缓存。正常关服后复制整个目录即可迁移。不会将地图、日志、个人设置或密钥提交到仓库。

ZIP 最大 2 GB、解压后最大 8 GB、最多 100000 条目，仅支持 Java 版地图。解压会检查越界路径、重复文件、链接和 Windows 保留名称。自动识别不能保证地图最低兼容版本，请确认作者说明。

第一版暂不支持多个实例同时运行、Java 自动安装、备份一键恢复、模组服、插件服及整合包。后续方向见 [ARCHITECTURE.md](ARCHITECTURE.md)。

## License

项目代码 MIT，第三方组件遵循各自许可。WebView2 是微软组件。Minecraft 商标属于各自权利人，本项目与 Mojang / Microsoft 无隶属关系，不分发 Minecraft 服务端二进制文件。
