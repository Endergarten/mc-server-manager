# 实现结构与后续扩展

## 当前实现

纯 Go 标准库 HTTP 服务，内嵌静态页面，通过 JSON API 管理本机实例。不依赖前端构建工具或数据库。

| 文件 | 职责 |
| --- | --- |
| `main.go` | 监听本机、网页资源、请求访问检查、正常退出 |
| `store.go` | 实例库、设置、原子文件写入、离线 UUID |
| `archive.go` | ZIP 安全解压、NBT 版本读取、JAR 内部版本识别 |
| `versions.go` | Mojang 版本列表、下载任务、进度与文件校验 |
| `server.go` | 准备配置、启动子进程、日志、stdin 命令、等待 stop |
| `api.go` | 页面操作 API、封面和停止状态备份 |
| `web/` | 无框架 HTML / CSS / JavaScript 界面 |

数据结构中的 `World` 是一个持有地图的开服实例，而不是只读地图模板；实例持有自己的存档和配置，并引用共用版本库。保留 `engine` 字段，当前唯一可运行类型为 `vanilla`。未知类型会被明确拒绝，不会误当作原版运行。

启动时验证实例存在、用户已同意 EULA、JAR 内部版本正确、端口未占用；应用默认配置和 OP 后，以 `exec.Command` 启动 Java，不拼接 shell 命令。日志写入磁盘并保留最近 800 行供页面轮询；保留 stdin 管道，后台命令直接写入 Java 进程。只有看到准备完成日志才显示运行中。

`stop` 进入“正在保存并停止”，等待子进程真实退出后才允许再次启动。退出管理器先禁止新的启动请求，等待最多 60 秒；超时保留管理器以便继续观察，不强杀服务器。

## 加入模组服、插件服时

不要仅用一个不同的 JAR 替换原版。建议把当前原版逻辑提取为一个服务端类型适配器，统一暴露版本列表、安装校验和启动计划三个能力：

```go
type ServerEngine interface {
    Versions(context.Context) ([]EngineVersion, error)
    Install(context.Context, InstallRequest) (InstalledServer, error)
    LaunchPlan(Instance, InstalledServer) (LaunchSpec, error)
}
```

上面的接口是后续设计示意，当前代码尚未实现。启动、日志、后台命令、备份和页面状态可以继续共用，避免对每种服务端重写一套管理逻辑。

新增实例信息应分别保存：游戏版本、服务端类型、加载器版本、每实例 Java 及启动参数。Fabric 有独立加载器版本，Forge / NeoForge 安装后可能需要参数文件而不是简单的 `java -jar`；不能把安装器当服务端直接执行。

整合包导入应先识别格式和清单（如 Modrinth `.mrpack`），展示需要下载的文件及服务端侧兼容情况，安装到独立的暂存目录，校验依赖后再完成实例创建。不得自动执行整合包中附带的任意脚本。插件服还应检查插件支持的服务端和游戏版本。

`servers/` 未来按类型、游戏版本、加载器版本划分缓存；`instances/` 保持实例独立，`mods/`、`plugins/`、`config/` 随实例保存。更新前先创建完整备份，再安装替换，失败时回滚。
