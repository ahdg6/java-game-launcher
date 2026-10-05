# 架构与维护约定

## 依赖方向

```text
cmd/java-game-launcher
        │
        ▼
   internal/app ───────► internal/diagnostics
        │
        ├──────────────► internal/java ───────┐
        │                                     │
        └──────────────► internal/mindustry ──┤
        │                                     │
        └─────────────────────────────────────┤
                                              ▼
                                        internal/fsutil
```

- `cmd/java-game-launcher` 只保留 `main`，不得承载业务逻辑。
- `internal/app` 是可执行应用的组合根，负责配置、发现、启动编排和 Bubble Tea 状态机；视图文件只处理界面状态与领域 API 的调用。
- `internal/diagnostics` 负责纯日志分析，不依赖配置、进程或界面。
- `internal/java` 负责 Java 探测、JVM 预设、平台内存检测和经过校验的 Zulu 安装。
- `internal/mindustry` 负责游戏数据备份、模组状态和安全启动恢复，不依赖 TUI。
- `internal/fsutil` 只容纳被多个包复用且需要统一审计的文件系统安全原语。

新依赖必须沿图中箭头方向。若领域逻辑开始依赖界面模型，应先改成参数或返回值，而不是反向导入 `internal/app`。不要仅为了缩短文件而新建包；只有职责、依赖和测试边界能够同时说清时才拆包。

## 应用层边界

`internal/app` 内按职责组织，暂不引入额外框架或包层级：

- `application.go`：组合 CLI/TUI，接受参数及输入输出，返回退出码；只有 `cmd` 调用 `os.Exit`。
- `application_cli.go`：独立 `FlagSet`、CLI 执行与报告。预检查后启动复用同一份 `LaunchSpec`。
- `config.go` / `instances.go` / `config_store.go`：分别负责启动配置与路径、实例操作、配置解析与原子持久化。保存先复制快照，验证失败或写入失败都不修改调用者状态。
- `discovery.go` / `launch.go` / `preflight.go`：发现、启动计划与检查。Java 探测限制并发，支持上下文取消和超时；JAR 只读取 manifest 主属性区来确定入口。
- `tui.go`：页面枚举、共享状态和消息分派；编辑器、菜单、日志、启动及其他页面各有职责文件。编辑器和文件选择器作为页面之上的临时交互状态。
- `launch_view.go`：把启动恢复、保存和 Java/JAR 检查放到异步命令中；请求绑定实例并携带身份，只有当前请求的结果可以启动进程。
- `logs.go` / `process.go`：日志会话和进程生命周期。输出、完成消息绑定会话；安全模式的准备与恢复捕获同一组目录，在会话内执行，恢复错误写入持久日志。

启动准备期间阻止更改配置、切换实例和退出；游戏运行期间继续保护实例与进程绑定关系。过期的发现、启动准备和进程消息不得覆盖当前状态。后台命令需要配置时，必须捕获独立快照，不能在后台访问可变的界面模型。

自动启动只属于 TUI 初次进入流程：本地检测完成后，依据独立的实例成功记录与启动配置指纹决定是否开启 3 秒倒计时。用户输入取消本次资格，计时消息携带归属标记，刷新、切页和失败都不得重新启动倒计时。每次启动尝试先使旧成功记录失效，普通启动正常退出后才重新建立；命令行诊断和预检查不能写入成功记录。

新增游戏适配器注册到 `gameAdapters` 后，配置验证和界面的候选列表从同一注册表派生，不再分别维护游戏 ID 列表。

## 命名

包名表达领域，符号和文件名表达职责：使用 `mindustry.Mod`、`mindustry.ScanMods`、`history_view.go`，避免 `MindustryMod`、`ui_history.go` 这类重复上下文。平台实现只使用 Go 构建标签认可的后缀，例如 `memory_windows.go` 和 `replace_other.go`。

## 变更检查

日常提交前运行：

```sh
mise run check
```

发布构建运行：

```sh
mise run build:all
```

本地和 CI 都从 `mise.toml` 读取固定 Go 版本和任务定义。`mise run ci` 包含提交检查、竞态测试和本机构建；竞态测试需要本机 C 编译器。

CI 在 Linux、Windows、macOS 执行 `mise run check`（格式、`vet`、测试），Linux 再执行 `mise run test:race`，通过后调用六个 `build:<平台>-<架构>` 任务。旧 `scripts/` 入口只转发到这些任务。`dist/` 是可再生成目录，不应提交游戏 JAR、Java 运行时或二进制成品。
