# 模块与扩展边界

当前正式发行目标只有 Windows 10 / 11。Windows 界面位于 `frontend/windows/`，已经独立重写为服务器列表、属性面板和底部控制台，不复用早期网页卡片。

## 通用逻辑

`store.go` 管理实例、设置与原子保存；`archive.go` 处理 ZIP、NBT 和 JAR 识别；`versions.go` 处理官方元信息及下载；`java.go` 负责 Java 描述、缓存、通用扫描和兼容匹配；`server.go` 管理进程、日志、后台命令和正常关服。`api.go`、`java_api.go` 为界面提供本地操作。

`World` 代表持有独立地图与配置的开服实例，引用共用的版本库。`engine` 当前仅支持 vanilla，未知类型明确拒绝启动。

## 桌面与平台模块

`internal/desktop` 不依赖开服核心类型，通过 HTTP Handler、运行状态、正常停止、文件浏览和地图拖入回调连接。默认桌面使用 Wails 内部资源处理，不开放独立 HTTP 端口。

平台相关代码放在 `internal/platform/windows`：Java 注册表与安装位置、WebView2、窗口配置、Java 子进程隐藏。根目录 `*_windows.go` 只保留薄转接；macOS 和 Linux 预留目录，不发布未测试的桌面包。

`frontend/windows` 是 Windows 风格界面模块，Go embed 直接嵌入，不需要 NPM。`scripts/windows` 是 Windows 打包模块。桌面构建使用 `desktop,production,wv2runtime.embed` 标签；默认测试不引入图形宿主，便于验证可移植核心。网页兼容模式仅保留用于开发测试。

## Java 发现与选择

后台搜索常见位置，读取 release 文件，Windows 可解析 PE 架构，必要时使用带超时的 Java 版本命令。浏览补充的 Java 或文件夹会持久记录。

匹配顺序为用户覆盖 → 官方元信息 → JAR 内 version.json → 正式版兼容表。只从 allowed 中选择，优先 preferred，再选择新补丁版本。未知快照不按名字猜测；没有兼容 Java 则提示补充。规则保存于可编辑的 java-rules.json，启动时重读。

## 生命周期

Java 命令参数直接交给 exec.Command，不拼接 shell。保留 stdin，支持后台命令。只有准备完成日志出现才显示运行；stop 后等待进程真正退出才能再次启动。

关闭桌面窗口时确认是否保存停止；用户取消则保留，确认后正常 stop 并等待。超时也不会强杀。兼容 HTTP 模式继续校验 Host / Origin / 写令牌。

## 后续类型

Fabric、Forge、NeoForge、Paper 应分别实现版本列表、安装校验和启动计划适配器，共用日志、命令和生命周期。游戏版本、服务端类型、加载器版本分开记录，不能将安装器直接当服务端运行。

整合包先解析清单并核对服务端依赖，暂存下载校验后创建实例，不自动执行压缩包中的任意脚本。mods、plugins、config 跟随实例保存，更新前备份。
