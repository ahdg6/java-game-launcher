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

## 命名

包名表达领域，符号和文件名表达职责：使用 `mindustry.Mod`、`mindustry.ScanMods`、`history_view.go`，避免 `MindustryMod`、`ui_history.go` 这类重复上下文。平台实现只使用 Go 构建标签认可的后缀，例如 `memory_windows.go` 和 `replace_other.go`。

## 变更检查

日常提交前运行：

```sh
./scripts/check.sh
```

发布构建运行：

```sh
./scripts/build-all.sh
```

CI 会在 Linux 执行普通测试、竞态测试和 `go vet`，在 Windows/macOS 再执行平台测试，并对六个目标平台分别构建。`dist/` 是可再生成目录，不应提交游戏 JAR、Java 运行时或二进制成品。
