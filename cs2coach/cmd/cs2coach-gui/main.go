// Command cs2coach-gui 是 CS2 复盘教练的桌面图形版入口（Wails v2）。
//
// 构建：
//
//	wails build                                 # 本机平台（macOS 出 .app）
//	wails build -platform windows/amd64 -nsis   # Windows 安装包（需交叉工具链）
//
// 窗口与绑定配置在 gui/run.go；工程根目录的 main.go 是同一入口的转发，
// 因为 Wails CLI 直接 `go build .`，要求根目录有 main 包。
package main

import "cs2-demo-review/gui"

func main() {
	gui.Main()
}
