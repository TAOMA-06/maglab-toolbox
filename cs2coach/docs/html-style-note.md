# report.HTML 样式注记

自包含单文件 HTML（无外部字体/脚本依赖；字体栈写 `-apple-system, BlinkMacSystemFont, "PingFang SC", "Hiragino Sans GB", "Segoe UI", "Microsoft YaHei", sans-serif`；`tick` 与 `demo_gototick` 用 `ui-monospace, "SFMono-Regular", Menlo, Consolas, monospace`）。

- 背景 `#06090e`，卡片 `#0c121a` + 边框 `1px solid rgba(255, 255, 255, 0.075)`，圆角 12px
- 文字：主 `#e9eff7`（14px / 行高 22px），次 `#8b98aa`
- 唯一强调色 `#d7ff3f`（「要你改的」标题、比分、tick / `demo_gototick`、可复制命令）
- 标题行带小圆点/胶囊徽章（比分、地图、焦点玩家名）
- 丢分回合用卡片列表：回合号+tick 徽章（等宽字体，`demo_gototick` 可复制）
- 证据行用左侧色条 + `tick` 小徽章
- 页脚：工具说明 + 「证据引用架构锁」声明
- 响应式：`max-width: 1000px`，小屏可读
