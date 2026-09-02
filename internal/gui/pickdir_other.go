//go:build !windows

package gui

// pickDir 非 Windows 平台：回退到 Fyne 自带的文件夹选择对话框
// （仅用于跨平台编译与开发机调试；正式发布目标是 Windows）。
import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/dialog"
)

func (g *gui) pickDir() {
	dialog.ShowFolderOpen(func(lu fyne.ListableURI, err error) {
		if err != nil {
			g.errDialog("读取目录失败：" + err.Error())
			return
		}
		if lu != nil {
			g.dirEntry.SetText(lu.Path())
		}
	}, g.win)
}
