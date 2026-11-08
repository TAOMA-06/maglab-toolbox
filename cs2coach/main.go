// 工程根目录的 main 包：Wails CLI 在项目根执行 `go build .`，需要有可编译的 main 包。
// 真正的入口实现在 gui/run.go，cmd/cs2coach-gui 也转发到同一处。
//
//	wails build      # 生成 build/bin/cs2coach-gui.app（macOS）
package main

import "cs2-demo-review/gui"

func main() {
	gui.Main()
}
