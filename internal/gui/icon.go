package gui

import (
	_ "embed"

	"fyne.io/fyne/v2"
)

//go:embed icon/app.png
var appIconPNG []byte

// appIconFyne 返回 fyne 应用图标（窗口标题栏 / 任务栏 / 托盘图标共用）。
// 资源来自 internal/gui/icon/app.png，构建时由 //go:embed 打入二进制。
func appIconFyne() fyne.Resource {
	return fyne.NewStaticResource("app.png", appIconPNG)
}
