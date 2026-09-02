package gui

import (
	"os"

	"fyne.io/fyne/v2"
)

// loadCJKFont 从本机系统加载中文字体供 Fyne 使用。
//
// 设计说明：目标运行环境是 Windows 内网（Win10/11 必带以下字体），
// 因此不必把 CJK 字体打进 exe —— exe 更小、无字体再分发版权负担。
// 后续若需"完全自包含"，可改为 go:embed 的 OFL 字体并保持此函数签名。
//
// 候选顺序：simhei.ttf（黑体，单 face TTF，Fyne 兼容性最稳）→
// msyh.ttc（微软雅黑，观感更好）→ simsun.ttc（宋体兜底）。
// 返回 nil 表示未找到任何系统字体（回退 Fyne 默认西文字体）。
func loadCJKFont() fyne.Resource {
	candidates := []string{
		`C:\Windows\Fonts\simhei.ttf`,
		`C:\Windows\Fonts\msyh.ttc`,
		`C:\Windows\Fonts\simsun.ttc`,
	}
	for _, p := range candidates {
		b, err := os.ReadFile(p)
		if err != nil || len(b) == 0 {
			continue
		}
		// 资源名带 .ttf 后缀，让 Fyne 按 TrueType 解析（对 TTC 集合同样取首 face 尝试）。
		return fyne.NewStaticResource("cjk.ttf", b)
	}
	return nil
}
