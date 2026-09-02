# assets

这些文件在编译时通过 `go:embed` 打进 exe，产出的单文件才不用另带资源。

- `README.md`（本文件）：embed 至少要匹配一个文件，留着占位
- `templates/`：网页模板 —— `list.html` 目录列表页、`login.html` 密码登录页，由 `internal/server` 渲染
- `icon/`：`app.ico`（16~256 多尺寸），go-winres 写入 `.syso` 后链进 exe
- `icon_raw/`：图标源 —— 1024 PNG + `build_icons.py` 绘制脚本，改图标从这里重生成
- `embed.go`：`//go:embed README.md templates` 入口

> 中文字体不走内嵌：运行时加载 Windows 系统字体（见 internal/gui/fonts.go），
> exe 更小，也避开字体再分发的版权问题。
