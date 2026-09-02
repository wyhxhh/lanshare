// Package assets 集中管理所有内嵌资源（go:embed），
// 保证最终产物是真正自包含的单文件 exe。
//
// 说明：
//   - 中文字体策略：运行时加载 Windows 系统字体（见 internal/gui/fonts.go），
//     不内嵌 CJK 字体 —— exe 更小且规避字体再分发版权问题。
//   - templates/ 为 internal/server 的目录列表页/登录页网页模板。
package assets

import (
	"embed"
	"io/fs"
)

//go:embed README.md templates
var embedded embed.FS

// Read 返回内嵌资源内容（相对本包根目录的路径）。
func Read(name string) ([]byte, error) {
	return fs.ReadFile(embedded, name)
}
