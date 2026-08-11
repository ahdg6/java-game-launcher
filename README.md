# Java Game Launcher

一个轻量的跨平台 Java 游戏 TUI 启动器，使用 Go、Bubble Tea 构建。支持 Windows、Linux 和 macOS，可自动发现便携 JRE/JDK、检查 JAR 所需 Java 版本和运行时架构，并管理 JVM 参数、游戏参数、数据目录与启动日志。

目前内置两个游戏配置：

- `auto`：根据 JAR 的 `Main-Class` 自动识别；
- `generic`：适用于普通可执行 Java JAR；
- `mindustry`：在通用功能上增加 `mindustry.data.dir` 与 Arc/SDL 原生库架构检查。

## 使用方式

把启动器、游戏 JAR 和可选的便携 Java 放在同一目录：

```text
Game/
├─ java-game-launcher-windows-amd64.exe
├─ Game.jar
├─ game_data/
└─ openjdk-21/
   └─ bin/
      └─ java.exe
```

然后双击启动器或从终端运行。Java 的发现优先级为：

1. 配置文件指定的 Java；
2. 启动器和配置目录附近的 `*/bin/java(.exe)`，最多向下 4 层；
3. `JAVA_HOME/bin/java(.exe)`；
4. 系统 `PATH`。

启动器读取 JAR 的 `Main-Class` 及 class major version，从而确定最低 Java 版本。适配器还可以声明游戏自带的原生库架构；例如 Mindustry 配置会拒绝用 32 位 Java 加载只包含 64 位 Arc/SDL 库的 JAR。

若启动器运行在 Flatpak 开发工具中，会自动通过 `flatpak-spawn --host` 进入宿主图形会话，避免 SDL/LWJGL 游戏无法访问显示设备。

## TUI 操作

- `↑` / `↓` 或 `J` / `K`：选择菜单；
- `Enter`：执行或编辑；
- Java、JAR、游戏配置项上按 `←` / `→`：切换候选项；
- 文件选择器中用 `Enter` 进入目录或选择文件，`S` 选择当前目录，`M` 手动输入，`C` 清空；
- 参数编辑器中每行是一个完整参数，`Ctrl+S` 接受，`Esc` 取消；
- `D`：选中 JVM 参数时恢复低停顿默认值；
- `S` 保存，`R` 重新检测，`Q` 退出。

游戏的 stdout/stderr 会实时显示在可滚动日志页中。方向键、`PgUp`/`PgDn`、`g`/`G` 可浏览日志，完整日志保存在配置目录的 `logs/` 中。

## 游戏配置与特殊参数

通用核心只负责 Java/JAR 发现、兼容性、进程、配置和日志。游戏特殊行为通过 [`GameAdapter`](./game_adapter.go) 隔离：

```go
type GameAdapter interface {
    ID() string
    DisplayName() string
    Matches(mainClass string) bool
    DataDirectoryProperty() string
    NativeArchitectures(files []*zip.File, goos string) []string
    LaunchArguments(context AdapterLaunchContext) (jvmArgs, gameArgs []string, err error)
}
```

添加新游戏时可实现适配器并注册到 `gameAdapters`。`LaunchArguments` 可以分别在 `-jar` 前后追加专用参数；接口还可继续扩展必需模块和游戏专属校验，而无需修改 Java 发现、进程与 TUI 日志核心。

没有专用适配器的游戏使用 `generic`：JVM 参数和游戏参数仍然完全可编辑，游戏参数会放在 `-jar Game.jar` 之后。特殊的 `-D` 属性可以直接写进 JVM 参数。

### Mindustry 数据目录

Mindustry 配置首次默认使用 JAR 旁的 `game_data`，自动创建并传入：

```text
-Dmindustry.data.dir=/绝对路径/game_data
```

在 TUI 清空数据目录后不会添加该属性，Mindustry 将使用自身默认位置。相对数据目录始终以 JAR 所在目录为基准。

## 默认 JVM 参数

默认值面向 Java 17+ 游戏的低停顿和帧时间稳定性：

```text
-Xms2g
-Xmx2g
-XX:+UseG1GC
-XX:MaxGCPauseMillis=30
-XX:G1ReservePercent=20
-XX:+ParallelRefProcEnabled
-XX:+AlwaysPreTouch
-XX:+DisableExplicitGC
-Dfile.encoding=UTF-8
```

固定 2 GiB 堆可避免运行时扩堆，`AlwaysPreTouch` 将内存缺页成本提前到启动阶段。大型游戏或模组包可同时提高 `-Xms`、`-Xmx`，但不建议超过物理内存的一半。对于 Java 版本较旧或需求不同的游戏，应在 TUI 中调整或清空这些参数。

## 命令行模式

```sh
java-game-launcher --launch
java-game-launcher --dry-run
java-game-launcher --diagnose
java-game-launcher --launch -- -debug
java-game-launcher --config ./custom.json
```

## 配置格式

默认配置文件为 `java-game-launcher.json`。若同目录只有旧版 `mindustry-launcher.json`，会自动读取并在保存时迁移。

```json
{
  "version": 4,
  "game_profile": "auto",
  "java_path": "openjdk-21/bin/java.exe",
  "jar_path": "MindustryX-Desktop.jar",
  "working_directory": "",
  "data_directory": "game_data",
  "jvm_args": ["-Xms2g", "-Xmx2g", "-XX:+UseG1GC"],
  "game_args": []
}
```

Java、JAR 和工作目录的相对路径按配置文件目录解析；数据目录相对 JAR 所在目录解析。通用配置不会使用专用数据目录字段。

## 构建

需要 Go 1.24 或更新版本：

```sh
go test ./...
go build -trimpath -o java-game-launcher .
```

一次构建 Windows、Linux、macOS 的 amd64/arm64 版本：

```sh
chmod +x scripts/build-all.sh
./scripts/build-all.sh
```

Windows PowerShell：

```powershell
.\scripts\build-windows.ps1
```

构建使用 `CGO_ENABLED=0`，不需要额外 DLL 或 C 编译器。游戏 JAR 与 JRE/JDK 不会打包进启动器。
