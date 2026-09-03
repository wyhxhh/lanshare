package gui

// LanShare 主题：品牌配色 + CJK 字体。
// 设计取向：桌面工具固定浅色（现代分享/网盘类应用的通用审美），
// 不跟随系统深色 —— 风格统一、避免深色下自绘组件出现未适配角落。

import (
	"image/color"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/theme"
)

// ---------- 品牌与界面色板（浅色） ----------

var (
	// 结构色
	colBG       = color.NRGBA{0xEE, 0xF1, 0xF7, 0xFF} // 窗口底：浅灰蓝
	colCard     = color.NRGBA{0xFF, 0xFF, 0xFF, 0xFF} // 卡片：纯白
	colBorder   = color.NRGBA{0xE0, 0xE6, 0xF0, 0xFF} // 卡片描边
	colLine     = color.NRGBA{0xEC, 0xEF, 0xF5, 0xFF} // 卡内分隔细线
	colHover    = color.NRGBA{0xED, 0xF2, 0xFF, 0xFF} // 悬停
	colPressed  = color.NRGBA{0xD9, 0xE4, 0xFF, 0xFF} // 按下
	colInputBd  = color.NRGBA{0xC9, 0xD2, 0xE0, 0xFF} // 输入框描边
	colDisabled = color.NRGBA{0x8B, 0x97, 0xA9, 0xFF} // 禁用文本（≥3:1，仍可辨认内容）

	// 品牌与状态色
	colPrimary     = color.NRGBA{0x2F, 0x6B, 0xFF, 0xFF} // 主蓝
	colPrimaryDeep = color.NRGBA{0x1D, 0x54, 0xE6, 0xFF} // 按下主按钮
	colOnPrimary   = color.NRGBA{0xFF, 0xFF, 0xFF, 0xFF}
	colGreen       = color.NRGBA{0x17, 0xA0, 0x5C, 0xFF} // 运行中
	colGreenBg     = color.NRGBA{0xE4, 0xF6, 0xEC, 0xFF}
	colGreenDep    = color.NRGBA{0x0D, 0x74, 0x43, 0xFF} // 运行中文案
	colChipIdle    = color.NRGBA{0xE9, 0xED, 0xF3, 0xFF} // 未启动胶囊底
	colCodeBg      = color.NRGBA{0xF6, 0xF8, 0xFB, 0xFF} // 日志/地址浅底

	// 文字四级色阶（浅色底，全部 ≥ WCAG AA）：
	//   主文本 ≥12:1 → 次要文本 ~7:1 → 弱提示/占位 ~5:1 → 禁用 ~3:1
	colInk = color.NRGBA{0x1B, 0x24, 0x34, 0xFF} // 主文本
	colSub = color.NRGBA{0x46, 0x54, 0x6B, 0xFF} // 次要文本（字段标签/说明）
	colDim = color.NRGBA{0x5F, 0x6E, 0x88, 0xFF} // 弱提示/占位符/副标（原 #94A0B4 仅 2.6:1 过浅）
)

// light 主题色映射：把 Fyne 组件所需 ColorName 全部映射到品牌色板，
// 未列出的名字回退默认主题（保证新组件不"穿错色"）。
var lightMap = map[fyne.ThemeColorName]color.Color{
	theme.ColorNameBackground:                colBG,
	theme.ColorNameButton:                    color.NRGBA{0xEE, 0xF1, 0xF7, 0xFF}, // 普通按钮：窗底色（hover 时变浅蓝）
	theme.ColorNameDisabled:                  colDisabled,
	theme.ColorNameDisabledButton:            color.NRGBA{0xEC, 0xEF, 0xF5, 0xFF},
	theme.ColorNameError:                     color.NRGBA{0xE5, 0x48, 0x4D, 0xFF},
	theme.ColorNameFocus:                     colPrimary, // 输入聚焦描边
	theme.ColorNameForeground:                colInk,
	theme.ColorNameForegroundOnPrimary:       colOnPrimary,
	theme.ColorNameForegroundOnError:         color.NRGBA{0xFF, 0xFF, 0xFF, 0xFF},
	theme.ColorNameForegroundOnSuccess:       color.NRGBA{0xFF, 0xFF, 0xFF, 0xFF},
	theme.ColorNameForegroundOnWarning:       color.NRGBA{0x1B, 0x24, 0x34, 0xFF},
	theme.ColorNameHeaderBackground:          colCard,
	theme.ColorNameHover:                     colHover,
	theme.ColorNameHyperlink:                 colPrimary,
	theme.ColorNameInnerWindowBorder:         colBorder,
	theme.ColorNameInnerWindowBorderInactive: color.NRGBA{0xE8, 0xEC, 0xF3, 0xFF},
	theme.ColorNameInputBackground:           color.NRGBA{0xFB, 0xFC, 0xFE, 0xFF},
	theme.ColorNameInputBorder:               colInputBd,
	theme.ColorNameMenuBackground:            colCard,
	theme.ColorNameOverlayBackground:         color.NRGBA{0xFF, 0xFF, 0xFF, 0xE6}, // 对话框遮罩底
	theme.ColorNamePlaceHolder:               colDim,
	theme.ColorNamePressed:                   colPressed,
	theme.ColorNamePrimary:                   colPrimary,
	theme.ColorNameScrollBar:                 color.NRGBA{0xC1, 0xCA, 0xD9, 0xFF},
	theme.ColorNameScrollBarBackground:       color.NRGBA{0x00, 0x00, 0x00, 0x00},
	theme.ColorNameSelection:                 color.NRGBA{0xCF, 0xE0, 0xFF, 0xFF},
	theme.ColorNameSeparator:                 colLine,
	theme.ColorNameShadow:                    color.NRGBA{0x10, 0x1E, 0x3C, 0x28},
	theme.ColorNameSuccess:                   colGreen,
	theme.ColorNameWarning:                   color.NRGBA{0xE8, 0x8A, 0x1A, 0xFF},
}

// lanTheme 实现 fyne.Theme：固定浅色品牌配色 + CJK 字体。
type lanTheme struct {
	base fyne.Theme // 默认主题（提供图标与未覆盖色）
	font fyne.Resource
}

func (t *lanTheme) Color(name fyne.ThemeColorName, _ fyne.ThemeVariant) color.Color {
	if c, ok := lightMap[name]; ok {
		return c
	}
	return t.base.Color(name, theme.VariantLight)
}

func (t *lanTheme) Font(style fyne.TextStyle) fyne.Resource {
	if t.font != nil {
		return t.font
	}
	return t.base.Font(style)
}

func (t *lanTheme) Icon(name fyne.ThemeIconName) fyne.Resource {
	return t.base.Icon(name)
}

func (t *lanTheme) Size(name fyne.ThemeSizeName) float32 {
	switch name {
	case theme.SizeNamePadding:
		return 5 // 全局间距稍宽松（默认 4）
	case theme.SizeNameSeparatorThickness:
		return 1
	}
	return t.base.Size(name)
}

// newLanTheme 以默认主题为基底构造品牌主题（base 提供图标与未覆盖色）。
func newLanTheme(font fyne.Resource) *lanTheme {
	return &lanTheme{base: theme.DefaultTheme(), font: font}
}
