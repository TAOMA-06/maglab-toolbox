package gui

import (
	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/mac"
	"github.com/wailsapp/wails/v2/pkg/options/windows"
)

// Main 启动桌面应用（Wails 窗口 + 绑定 App）。
//
// 入口集中在这里，工程根目录的 main.go 与 cmd/cs2coach-gui/main.go 都只做转发：
// Wails CLI 要求在工程根目录能编译 main 包（它不带包参数，直接 `go build .`），
// 同时保留 cmd/cs2coach-gui 作为显式入口。
func Main() {
	app := NewApp()

	err := wails.Run(&options.App{
		Title:     "CS2 复盘教练",
		Width:     1280,
		Height:    860,
		MinWidth:  1040,
		MinHeight: 680,
		AssetServer: &assetserver.Options{
			Assets: Assets,
		},
		BackgroundColour: &options.RGBA{R: 2, G: 6, B: 23, A: 1},
		OnStartup:        app.Startup,
		Bind:             []interface{}{app},
		// 允许把 .dem 拖进窗口：EnableFileDrop 后由 App.Startup 里的 runtime.OnFileDrop 收路径。
		DragAndDrop: &options.DragAndDrop{
			EnableFileDrop:     true,
			DisableWebViewDrop: true,
		},
		Windows: &windows.Options{
			WebviewIsTransparent: false,
			WindowIsTranslucent:  false,
			Theme:                windows.Dark,
		},
		Mac: &mac.Options{
			Appearance:           mac.NSAppearanceNameDarkAqua,
			WebviewIsTransparent: false,
			WindowIsTranslucent:  false,
			About: &mac.AboutInfo{
				Title:   "CS2 复盘教练",
				Message: "本地 CS2 demo 复盘：事实由规则层产出，LLM 只解读不发明。",
			},
		},
	})
	if err != nil {
		println("Error:", err.Error())
	}
}
