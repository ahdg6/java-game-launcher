# Java Game Launcher

一个 Mindustry-first 的跨平台 Java 游戏 TUI 启动器，使用 Go、Bubble Tea 构建。支持 Windows、Linux 和 macOS，可自动发现便携 JRE/JDK、检查 JAR 所需 Java 版本和运行时架构，并管理多实例、JVM 参数、游戏数据、模组、备份、服务器控制台与实时日志。通用层只保留对启动流程可控的可执行 Java JAR 的轻量支持；Minecraft 等需要版本资产、认证和依赖解析的生态明确不在范围内。

目前内置三个游戏配置：

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

主菜单第一项是实例。可用 `←` / `→` 快速切换，或进入管理页后新建、克隆、重命名、二次确认删除及排序。实例 ID 自动生成且重命名后保持稳定；每个实例独立保存 Java、JAR、工作目录、数据目录和参数。克隆实例会自动改用独立的 `instances/<id>/game_data`，若高级用户让两个实例共用同一数据目录，TUI 会明确警告。

游戏的 stdout/stderr 会实时显示在可滚动日志页中。方向键、`PgUp`/`PgDn`、`g`/`G` 可浏览日志，完整日志按实例保存在配置目录的 `logs/<instance-id>/` 中；启动器重开或切换实例时会自动载入该实例最新日志的尾部。TUI 展示层会剥离 ANSI/终端控制序列，避免游戏颜色码清屏或扰乱界面；文件仍保留原始输出。启动失败不会退出或清空 TUI，诊断、原始输出和持久日志路径都留在日志页。

Mindustry Server JAR 会自动识别为无图形交互会话。日志页按 `I` 输入服务器命令；`Ctrl+X` 第一次发送 `exit` 让服务器保存并安全退出，再按一次才强制终止。游戏或服务器运行时，启动器会阻止切换实例和正常退出，避免日志、控制台或安全恢复状态绑定到错误实例。

启动失败后，日志页会在原始输出前展示智能诊断和修复建议。目前可识别 Java 版本/架构、缺失模块或类、无效 JVM 参数、内存不足、图形环境、权限和原生库问题；按 `D` 可切换诊断与原始日志。Mindustry 不依赖 JavaFX，因此启动器不会要求 JavaFX；它会检查实际需要的标准运行时模块 `java.desktop` 和 `jdk.unsupported`，从而拒绝缺模块的精简 jlink 运行时。

## 游戏配置与特殊参数

通用核心只负责 Java/JAR 发现、兼容性、进程、配置和日志。游戏特殊行为通过 [`GameAdapter`](./game_adapter.go) 隔离：

```go
type GameAdapter interface {
    ID() string
    DisplayName() string
    Matches(mainClass string) bool
    DataDirectoryProperty() string
    RequiredJavaModules(mainClass string) []string
    NeedsGraphics(mainClass string) bool
    InteractiveConsole(mainClass string) bool
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

### Mindustry 工具

主菜单的 Mindustry 工具页可以打开数据、模组和备份目录，并创建安全 ZIP 备份。备份按实例保存在 `backups/<instance-id>/`，保留存档、设置、模组、地图和未知用户数据，排除可再生的 `cache`、`tmp`，且不会跟随符号链接。

备份管理页会先用与恢复相同的安全规则检查 ZIP，再预览文件数、解压大小和顶层内容。恢复需要二次确认；覆盖当前同名文件前，启动器会先自动创建一份当前数据保护备份。恢复底层使用完整暂存、Zip Slip/符号链接/CRC 防护，验证或冲突失败不会发布部分目录。

模组管理页支持目录、JAR、ZIP 和 `.disabled`，会读取 `mod.json`、`plugin.json`、`mod.hjson` 中的名称、版本和作者。单项启停使用可逆重命名；批量禁用要求二次确认，并可在当前会话恢复。游戏运行期间禁止改变模组状态。

“无模组安全启动（仅本次）”会先原子写入恢复计划，再临时禁用全部已启用模组。游戏退出后自动恢复；若启动器或机器中断，下次启动会依据带校验和且绑定实例的恢复标记继续恢复。恢复标记还会绑定实际游戏进程；若另一启动器发现该进程仍存活，会保留禁用状态而不会在运行中的游戏下方提前恢复模组。任何路径冲突、越界或符号链接异常都会停止恢复而不是猜测或删除文件。

### 启动前完整检查

主菜单“启动前检查”复用真实启动计划，检查 Java 版本/架构、JAR Main-Class、Mindustry 必需模块、原生库兼容性、工作与数据目录写权限、图形会话，以及 JVM 参数试运行。试运行会临时覆盖为 16–64 MiB 小堆并跳过 `AlwaysPreTouch`，因此能发现无效选项而不会为了检查触碰数 GiB 内存。检查通过后可直接按 `Enter` 启动。

## 默认 JVM 参数

默认值面向 Java 17+ 游戏的低停顿和帧时间稳定性。新配置使用自动预设：物理内存少于 8 GiB 使用 1 GiB 堆、8–16 GiB 使用 2 GiB、16 GiB 以上使用 4 GiB。也可在 TUI 左右切换保守、均衡、性能预设；手动编辑参数后标记为自定义。

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

固定大小堆可避免运行时扩堆，`AlwaysPreTouch` 将内存缺页成本提前到启动阶段。大型游戏或模组包可提高预设，但不建议超过物理内存的一半。对于 Java 版本较旧或需求不同的游戏，应在 TUI 中调整或清空这些参数。

## 命令行模式

```sh
java-game-launcher --launch
java-game-launcher --dry-run
java-game-launcher --diagnose
java-game-launcher --preflight
java-game-launcher --instance server --launch
java-game-launcher --launch -- -debug
java-game-launcher --config ./custom.json
```

`--launch` 保留终端 stdin/stdout/stderr（服务器仍可直接输入命令），同时也写入按实例隔离的持久日志；异常退出会在终端末尾打印同一套智能诊断与日志路径。命令行追加的 `-- 游戏参数` 只影响本次运行，不会写回实例配置。

## 配置格式

默认配置文件为 `java-game-launcher.json`。若同目录只有旧版 `mindustry-launcher.json`，会自动读取并在保存时迁移。

```json
{
  "version": 6,
  "active_instance_id": "desktop",
  "instances": [
    {
      "id": "desktop",
      "name": "原版桌面",
      "game_profile": "mindustry",
      "java_path": "openjdk-21/bin/java.exe",
      "jar_path": "MindustryX-Desktop.jar",
      "working_directory": "",
      "data_directory": "game_data",
      "jvm_preset": "auto",
      "jvm_args": ["-Xms2g", "-Xmx2g", "-XX:+UseG1GC"],
      "game_args": []
    },
    {
      "id": "server",
      "name": "本地服务器",
      "game_profile": "mindustry",
      "java_path": "openjdk-21/bin/java.exe",
      "jar_path": "server-release.jar",
      "working_directory": "server",
      "data_directory": "instances/server/game_data",
      "jvm_preset": "auto",
      "jvm_args": ["-Xms2g", "-Xmx2g", "-XX:+UseG1GC"],
      "game_args": []
    }
  ]
}
```

v1–v5 会在内存中无损迁移成一个 `default` 实例，加载本身不会改写文件。第一次保存 v6 时，同名旧配置会保留为 `.v5.bak`；旧的 `mindustry-launcher.json` 则原样保留并写入新的 `java-game-launcher.json`。配置替换在 Unix 上使用同步后的原子 rename，在 Windows 上使用 `MoveFileExW(REPLACE_EXISTING | WRITE_THROUGH)`。

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
