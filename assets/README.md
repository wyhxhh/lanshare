# assets 内嵌资源目录

本目录内容通过 `go:embed` 打进单文件 exe，保证产物自包含。

- `README.md`（本文件）：占位，确保 embed 指令有匹配文件。
- `templates/`：HTTP 网页模板 —— `list.html` 目录列表页、`login.html` 密码登录页，
  由 `internal/server` 渲染。
- `icon/`：Windows exe 资源图标 `app.ico`（16~256 多尺寸），
  由 go-winres 写入 `.syso` 链入 exe。
- `icon_raw/`：图标源 —— `app-1024.png` 高清 PNG + `build_icons.py`
  程序化绘制脚本（可随时重生成 icon 与 .ico）。
- `embed.go`：`//go:embed README.md templates` 资源入口。

> 中文字体策略：运行时加载 Windows 系统字体（见 internal/gui/fonts.go），
> 不内嵌 CJK 字体 —— exe 更小且规避字体再分发版权问题。
