package gui

// 视觉组件助手：白卡、间隙、小标签等，供主布局拼装。

import (
	"image/color"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"
)

// card 白色圆角卡片（细描边），内容四周留 pad。
func card(content fyne.CanvasObject) fyne.CanvasObject {
	return cardP(18, 18, 18, 18, content)
}

// cardP 带自定义内边距的卡片（top, bottom, left, right）。
func cardP(pt, pb, pl, pr float32, content fyne.CanvasObject) fyne.CanvasObject {
	bg := canvas.NewRectangle(colCard)
	bg.CornerRadius = 14
	bg.StrokeColor = colBorder
	bg.StrokeWidth = 1
	inner := container.New(layout.NewCustomPaddedLayout(pt, pb, pl, pr), content)
	return container.NewStack(bg, inner)
}

// hbox 便捷水平容器（子项按主题间距排列）。
func hbox(objs ...fyne.CanvasObject) fyne.CanvasObject {
	return container.NewHBox(objs...)
}

// vbox 便捷垂直容器。
func vbox(objs ...fyne.CanvasObject) fyne.CanvasObject {
	return container.NewVBox(objs...)
}

// vgap 制造 n 像素高的垂直空隙（透明，不拦截事件）。
func vgap(n float32) fyne.CanvasObject {
	return container.New(layout.NewCustomPaddedLayout(n, 0, 0, 0), canvas.NewRectangle(color.Transparent))
}

// hgap 制造 n 像素宽的水平空隙。
func hgap(n float32) fyne.CanvasObject {
	return container.New(layout.NewCustomPaddedLayout(0, 0, n, 0), canvas.NewRectangle(color.Transparent))
}

// text 生成固定颜色/字号文本（仅静态内容）。
func text(s string, c color.Color, size float32) *canvas.Text {
	t := canvas.NewText(s, c)
	t.TextSize = size
	return t
}

// fieldLabel 表单字段的上置小标签。
func fieldLabel(s string) *canvas.Text {
	return text(s, colSub, 12)
}

// titleText 卡片/区块标题。
func titleText(s string) *canvas.Text {
	return text(s, colInk, 16)
}

// hintText 卡头右侧的辅助说明（小号灰字）。
func hintText(s string) *canvas.Text {
	return text(s, colDim, 12)
}

// hairline 1px 水平细分隔线（VBox/HBox 中会横向/纵向拉伸）。
func hairline(c color.Color) fyne.CanvasObject {
	return container.New(layout.NewCustomPaddedLayout(1, 0, 0, 0), canvas.NewRectangle(c))
}

// statusChip 顶栏运行状态胶囊：浅色圆角底 + 状态圆点 + 文案。
// 圆点与文字随 set() 同步变色，用于"未启动 / 运行中"即时反馈。
type statusChip struct {
	bg    *canvas.Rectangle
	dot   *canvas.Text
	label *canvas.Text
	run   bool
	txt   string
}

func newStatusChip() (*statusChip, fyne.CanvasObject) {
	bg := canvas.NewRectangle(colChipIdle)
	bg.CornerRadius = 13
	dot := text("●", colDim, 12)
	lb := text("未启动", colSub, 13)
	inner := container.New(layout.NewCustomPaddedLayout(4, 4, 11, 11), hbox(dot, hgap(6), lb))
	c := &statusChip{bg: bg, dot: dot, label: lb}
	return c, container.NewStack(bg, inner)
}

// set 更新胶囊（颜色仅在运行态切换时变，文案相同则跳过以避免无谓重绘）。
func (c *statusChip) set(run bool, txt string) {
	if run == c.run && txt == c.txt {
		return
	}
	c.run, c.txt = run, txt
	if run {
		c.bg.FillColor = colGreenBg
		c.dot.Color = colGreen
		c.label.Color = colGreenDep
	} else {
		c.bg.FillColor = colChipIdle
		c.dot.Color = colDim
		c.label.Color = colSub
	}
	c.label.Text = txt
	c.bg.Refresh()
	c.dot.Refresh()
	c.label.Refresh()
}
