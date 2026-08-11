# Roadmap

Java Game Launcher 的定位是：为 Mindustry 提供一等启动体验，同时支持少量“发行形式清晰、启动流程可控”的可执行 Java 游戏。Minecraft 版本资源、认证、资产和依赖解析明确不在范围内，它更适合专用启动器。

## 已完成

- 便携 Java/JAR 自动发现、class 版本和原生架构检查；
- 自动/保守/均衡/性能 JVM 预设及物理内存检测；
- Mindustry 数据目录、完整 JRE 模块预检和 Flatpak 图形桥接；
- 实时日志、持久日志和常见启动失败智能诊断；
- 数据安全备份/恢复底层服务；
- Mindustry 模组扫描、安全启停和批量回滚；
- Windows、Linux、macOS 的 amd64/arm64 CI 构建。

## 下一阶段

1. 多实例：原版、模组、测试版、服务器分别保存 Java/JAR、数据目录和参数。
2. Mindustry Server：无图形启动、TUI 控制台输入、停止/重启和服务器预设。
3. 数据恢复 UI：选择备份、预览内容、冲突确认后恢复。
4. 一次性安全模式：临时禁用模组启动，进程结束后自动恢复。
5. 启动前完整检查页：JVM 参数试运行、目录权限、模块和图形会话汇总。
6. 可校验下载：按 checksum 安装受信任的 Java LTS；游戏更新保持显式确认。

## 适配器候选

- Unciv、Shattered Pixel Dungeon Desktop：优先验证通用可执行 JAR 路径；
- RuneLite：可控的自举/缓存适配；
- Slay the Spire / ModTheSpire：在启动计划支持 classpath/外部原生库后考虑；
- Minecraft、Project Zomboid 等复杂发行系统不作为近期目标。
