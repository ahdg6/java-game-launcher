# Roadmap

Java Game Launcher 的定位是：为 Mindustry 提供一等启动体验，同时支持少量“发行形式清晰、启动流程可控”的可执行 Java 游戏。Minecraft 版本资源、认证、资产和依赖解析明确不在范围内，它更适合专用启动器。

## 已完成

- 便携 Java/JAR 自动发现、class 版本和原生架构检查；
- 自动/保守/均衡/性能 JVM 预设及物理内存检测；
- Mindustry 数据目录、完整 JRE 模块预检和 Flatpak 图形桥接；
- 实时日志、持久日志和常见启动失败智能诊断；
- 数据安全备份/恢复底层服务；
- Mindustry 模组扫描、安全启停和批量回滚；
- v6 多实例配置、v1–v5 无损迁移、稳定实例 ID 和共享数据目录警告；
- Mindustry Server 实时控制台、安全退出与强制停止；
- 一次性无模组安全启动及崩溃后持久恢复；
- 数据恢复 TUI：安全预览、二次确认和恢复前自动保护备份；
- 启动前完整检查页及 CLI `--preflight` JVM 参数试运行；
- Windows、Linux、macOS 的 amd64/arm64 CI 构建。

## 下一阶段

1. 服务器运维：重启、常用命令历史、定时/退出前备份和可选服务器模板。
2. 备份生命周期：保留数量/空间上限、删除确认和恢复内容过滤。
3. 启动历史：按实例浏览旧日志、比较失败诊断并导出脱敏诊断包。
4. 可校验下载：按 checksum 安装受信任的 Java LTS；游戏更新保持显式确认。
5. 适配器验证：只为发行形式清晰的 Unciv、Shattered Pixel Dungeon Desktop 等可执行 JAR 增加小型预检，不引入通用依赖解析器。

## 适配器候选

- Unciv、Shattered Pixel Dungeon Desktop：优先验证通用可执行 JAR 路径；
- RuneLite：可控的自举/缓存适配；
- Slay the Spire / ModTheSpire：在启动计划支持 classpath/外部原生库后考虑；
- Minecraft、Project Zomboid 等复杂发行系统不作为近期目标。
