// Package gui 提供桌面界面（Fyne 原生窗口）。
// HTTP 服务核心在 internal/server，与 GUI 完全解耦：
// GUI 只负责装配 Config、启动/停止服务、订阅日志与状态计数。
package gui

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/widget"

	"github.com/wyhxhh/lanshare/internal/server"
)

// appVersion 界面与网页页脚展示的版本号，发布前在此更新。
const appVersion = "0.2.0"

// maxLogLines 日志面板保留的最大行数（超出丢弃最旧）。
const maxLogLines = 2000

// errAlreadyRunning 单实例互斥冲突（已在运行）。
var errAlreadyRunning = errors.New("already running")

// disableable 是支持启停的控件集合（Entry/Button/Check 均实现）。
type disableable interface {
	Disable()
	Enable()
}

var (
	_ disableable = (*widget.Entry)(nil)
	_ disableable = (*widget.Button)(nil)
	_ disableable = (*widget.Check)(nil)
)

// Run 启动主窗口并阻塞至退出。
func Run() {
	// 单实例互斥：已有实例运行时提示并退出
	release, err := acquireSingleInstance()
	if err != nil {
		if errors.Is(err, errAlreadyRunning) {
			showAlreadyRunning()
		}
		return
	}
	defer release()

	a := app.NewWithID("com.lanshare.desktop")

	// 应用品牌图标（窗口标题栏 / 任务栏 / 托盘图标统一）
	a.SetIcon(appIconFyne())

	// 品牌主题：浅色配色 + 中文显示（找不到 CJK 字体则回退默认西文字体）
	a.Settings().SetTheme(newLanTheme(loadCJKFont()))

	g := &gui{app: a, logs: newLogBuffer(maxLogLines)}
	g.build()
	g.applyConfig(loadAppConfig())
	g.win.ShowAndRun()
}

type gui struct {
	app fyne.App
	win fyne.Window

	// 服务状态
	mu       sync.Mutex
	srv      *server.Server // 非 nil 表示正在运行
	lastURL  []string       // 上次启动的局域网访问地址列表
	done     chan struct{}  // 关闭信号：终止刷新协程
	quitOnce sync.Once      // 确保退出只执行一次

	// 设置表单
	dirEntry    *widget.Entry
	browseBtn   *widget.Button
	portEntry   *widget.Entry
	passEntry   *widget.Entry
	hiddenCheck *widget.Check
	ignoreEntry *widget.Entry

	// 控制
	startBtn *widget.Button
	stopBtn  *widget.Button
	openBtn  *widget.Button
	copyBtn  *widget.Button
	clearBtn *widget.Button

	// 展示
	chip       *statusChip       // 顶栏运行状态胶囊
	addrHint   fyne.CanvasObject // 地址区空态提示
	addrBody   *fyne.Container   // 地址区动态主体（空态提示 / 链接+指标）
	addrRich   *widget.RichText  // 运行后地址链接列表
	metricRow  *fyne.Container   // 指标行（运行时长/请求/下载/累计）
	metricVals [4]*canvas.Text   // 指标数值（与 metricRow 内顺序一致）
	logs       *logBuffer
	list       *widget.List

	// 需要随运行状态启停的输入控件
	inputs []disableable
}

// build 组装主窗口（一次调用，全部 UI 在 main 协程构造）。
func (g *gui) build() {
	w := g.app.NewWindow("LanShare")
	g.win = w
	g.done = make(chan struct{})
	w.SetPadded(false) // 布局自行控制留白

	// ---- 设置表单控件 ----
	g.dirEntry = widget.NewEntry()
	g.dirEntry.SetPlaceHolder("选择要共享的文件夹，如 D:\\share")
	g.browseBtn = widget.NewButton("浏览…", g.pickDir)

	g.portEntry = widget.NewEntry()
	g.portEntry.SetText(strconv.Itoa(server.DefaultPort))
	g.portEntry.SetPlaceHolder("1-65535，默认 8000")

	g.passEntry = widget.NewPasswordEntry()
	g.passEntry.SetPlaceHolder("留空则不设密码")

	g.hiddenCheck = widget.NewCheck("显示隐藏文件与 Office 临时文件", nil)

	g.ignoreEntry = widget.NewEntry()
	g.ignoreEntry.SetPlaceHolder("例如 *.tmp, node_modules, Thumbs.db")

	// ---- 控制按钮 ----
	g.startBtn = widget.NewButton("启动服务", g.start)
	g.startBtn.Importance = widget.HighImportance
	g.stopBtn = widget.NewButton("停止服务", g.stop)
	g.stopBtn.Importance = widget.DangerImportance
	g.openBtn = widget.NewButton("在浏览器打开", g.openBrowser)
	g.copyBtn = widget.NewButton("复制全部地址", g.copyURL)
	g.clearBtn = widget.NewButton("清空", func() {
		g.logs.clear()
		g.list.Refresh()
	})

	// ---- 顶栏状态胶囊 ----
	var chipObj fyne.CanvasObject
	g.chip, chipObj = newStatusChip()

	// ---- 地址区 ----
	g.addrRich = widget.NewRichText()
	g.addrRich.Wrapping = fyne.TextWrapWord
	g.addrHint = vbox(
		text("启动服务后，此处显示局域网访问地址", colSub, 13),
		text("同一网络下的手机 / 电脑用浏览器打开即可下载", colDim, 12),
	)
	g.addrBody = container.NewVBox(g.addrHint) // 初始为空态提示

	// ---- 访问日志列表（新日志在顶部，缓冲内也是新→旧） ----
	g.list = widget.NewList(
		func() int { return g.logs.len() },
		func() fyne.CanvasObject {
			l := widget.NewLabel("")
			l.TextStyle.Monospace = true
			l.Truncation = fyne.TextTruncateEllipsis
			return l
		},
		func(id widget.ListItemID, obj fyne.CanvasObject) {
			obj.(*widget.Label).SetText(g.logs.at(id))
		},
	)

	// ---- 整窗拼装 ----
	split := container.NewHSplit(
		vbox(g.buildSettingsCard(), layout.NewSpacer()),
		container.NewBorder(
			g.buildAddrCard(), // 上：访问地址卡（固定高）
			nil, nil, nil,
			g.buildLogCard(), // 中：日志卡占满剩余
		),
	)
	split.SetOffset(0.34)

	body := container.New(layout.NewCustomPaddedLayout(14, 14, 18, 18), split)
	w.SetContent(container.NewBorder(
		g.buildHeader(chipObj), // 顶栏（白底 + 底部分隔线）
		nil, nil, nil,
		body,
	))
	w.Resize(fyne.NewSize(1080, 700))

	// M3：关闭窗口 = 隐藏到托盘（服务继续运行）；真正退出走托盘菜单
	w.SetCloseIntercept(func() { w.Hide() })
	g.setupTray()

	g.inputs = []disableable{
		g.dirEntry, g.browseBtn, g.portEntry, g.passEntry,
		g.hiddenCheck, g.ignoreEntry,
	}
	g.stopBtn.Disable()
	g.openBtn.Disable()
	g.copyBtn.Disable()

	// 状态/日志刷新协程（读原子计数安全，UI 更新收敛到主协程）
	go g.refreshLoop()
}

// ---------- 视觉分区构建 ----------

// buildHeader 顶栏：左侧品牌标识，右侧运行状态胶囊。
func (g *gui) buildHeader(chip fyne.CanvasObject) fyne.CanvasObject {
	mark := canvas.NewRectangle(colPrimary)
	mark.CornerRadius = 3
	markWrap := container.New(layout.NewGridWrapLayout(fyne.NewSize(5, 34)), mark)

	title := text("LanShare", colInk, 19)
	sub := text("局域网文件共享 · v"+appVersion, colDim, 12)
	brand := hbox(markWrap, hgap(11), vbox(title, sub))

	row := container.New(layout.NewCustomPaddedLayout(10, 10, 22, 22),
		container.NewBorder(nil, nil, brand, chip))
	line := hairline(colBorder)
	return container.NewStack(
		canvas.NewRectangle(colCard),
		vbox(row, line),
	)
}

// buildSettingsCard 左栏"共享设置"卡。
func (g *gui) buildSettingsCard() fyne.CanvasObject {
	head := vbox(
		titleText("共享设置"),
		text("选择共享目录，点击「启动服务」", colDim, 12),
	)

	dirRow := container.NewBorder(nil, nil, nil,
		container.New(layout.NewCustomPaddedLayout(0, 0, 6, 0), g.browseBtn), g.dirEntry)

	fields := vbox(
		settingsField("共享目录", dirRow),
		settingsField("监听端口", g.portEntry),
		settingsField("访问密码", g.passEntry),
		container.New(layout.NewCustomPaddedLayout(7, 0, 0, 0), g.hiddenCheck),
		settingsField("忽略规则", g.ignoreEntry),
	)

	btnRow := container.NewBorder(nil, nil, g.startBtn, g.stopBtn)

	return card(vbox(
		head,
		container.New(layout.NewCustomPaddedLayout(11, 10, 0, 0), hairline(colLine)),
		fields,
		container.New(layout.NewCustomPaddedLayout(13, 0, 0, 0), btnRow),
	))
}

// buildAddrCard 右上"访问地址"卡：标题 + 操作按钮 + 动态主体。
func (g *gui) buildAddrCard() fyne.CanvasObject {
	head := hbox(
		titleText("访问地址"),
		layout.NewSpacer(),
		g.openBtn, hgap(6), g.copyBtn,
	)

	// 指标行：4 格数值卡（运行时填充）
	caps := []string{"运行时长", "活跃请求", "下载中", "累计发送"}
	for i := 0; i < 4; i++ {
		v := text("—", colDim, 18)
		g.metricVals[i] = v
	}
	cells := make([]fyne.CanvasObject, 0, 4)
	for i := 0; i < 4; i++ {
		cells = append(cells, vbox(g.metricVals[i], text(caps[i], colDim, 12)))
	}
	g.metricRow = container.New(layout.NewGridLayout(4), cells...)

	return card(vbox(
		head,
		container.New(layout.NewCustomPaddedLayout(12, 0, 0, 0), g.addrBody),
	))
}

// buildLogCard 右下"访问日志"卡：标题行 + 日志列表。
func (g *gui) buildLogCard() fyne.CanvasObject {
	head := hbox(
		titleText("访问日志"),
		layout.NewSpacer(),
		hintText(fmt.Sprintf("保留最近 %d 条", maxLogLines)),
		hgap(8), g.clearBtn,
	)

	zone := container.NewStack(
		func() fyne.CanvasObject {
			bg := canvas.NewRectangle(colCodeBg)
			bg.CornerRadius = 10
			return bg
		}(),
		container.New(layout.NewCustomPaddedLayout(4, 4, 4, 4), g.list),
	)

	return cardP(16, 16, 18, 18, container.NewBorder(
		container.New(layout.NewCustomPaddedLayout(0, 9, 0, 0), head),
		nil, nil, nil, zone,
	))
}

// settingsField 表单字段：上置小标签 + 控件。
func settingsField(label string, c fyne.CanvasObject) fyne.CanvasObject {
	return container.New(layout.NewCustomPaddedLayout(7, 0, 0, 0),
		vbox(fieldLabel(label), c))
}

// ---------- 配置回填 ----------

// applyConfig 把持久化配置回填到表单。
func (g *gui) applyConfig(cfg appConfig) {
	if cfg.Root != "" {
		g.dirEntry.SetText(cfg.Root)
	}
	if cfg.Port >= 1 && cfg.Port <= 65535 {
		g.portEntry.SetText(strconv.Itoa(cfg.Port))
	}
	g.passEntry.SetText(cfg.Password)
	g.hiddenCheck.SetChecked(cfg.ShowHidden)
	if len(cfg.Ignore) > 0 {
		g.ignoreEntry.SetText(strings.Join(cfg.Ignore, ", "))
	}
}

// ---------- 启动 / 停止 ----------

// start 校验设置并启动 HTTP 服务。
func (g *gui) start() {
	root := strings.TrimSpace(g.dirEntry.Text)
	if root == "" {
		g.errDialog("请先选择要共享的文件夹")
		return
	}
	if info, err := os.Stat(root); err != nil || !info.IsDir() {
		g.errDialog("共享目录不可用：" + root)
		return
	}

	port := server.DefaultPort
	if t := strings.TrimSpace(g.portEntry.Text); t != "" {
		n, err := strconv.Atoi(t)
		if err != nil || n < 1 || n > 65535 {
			g.errDialog("端口须为 1-65535 的整数")
			return
		}
		port = n
	}

	cfg := server.Config{
		Root:       root,
		Port:       port,
		Password:   g.passEntry.Text,
		ShowHidden: g.hiddenCheck.Checked,
		Ignore:     splitIgnore(g.ignoreEntry.Text),
		LogFn:      g.logFn,
	}
	srv, err := server.New(cfg, appVersion)
	if err != nil {
		g.errDialog("服务初始化失败：" + err.Error())
		return
	}
	if err := srv.Start(); err != nil {
		g.errDialog(err.Error())
		return
	}

	g.mu.Lock()
	g.srv = srv
	g.mu.Unlock()

	// 持久化本次设置
	pwDesc := "未设密码"
	if cfg.Password != "" {
		pwDesc = "已启用访问密码"
	}
	_ = saveAppConfig(appConfig{
		Root:       root,
		Port:       port,
		Password:   cfg.Password,
		ShowHidden: cfg.ShowHidden,
		Ignore:     cfg.Ignore,
	})
	g.logAdd(server.LogEntry{Time: time.Now(), Method: "GUI",
		Path: fmt.Sprintf("服务已启动：共享 %s · 端口 %d · %s", root, port, pwDesc)})

	// 展示局域网访问地址（可点击 + 可整段复制）
	urls := make([]string, 0, 4)
	for _, ip := range lanIPv4s() {
		urls = append(urls, fmt.Sprintf("http://%s:%d", ip, port))
	}
	if len(urls) == 0 {
		urls = append(urls, fmt.Sprintf("http://localhost:%d（未检测到局域网 IP）", port))
	}
	g.lastURL = urls
	g.showAddr(urls)

	g.openBtn.Enable()
	g.copyBtn.Enable()
	g.stopBtn.Enable()
	g.startBtn.Disable()
	for _, in := range g.inputs {
		in.Disable()
	}
	g.refreshNow()
}

// stop 停止 HTTP 服务（等活跃请求完成，最长 5 秒）。
func (g *gui) stop() {
	g.stopService(true)
	g.refreshNow()
}

// stopService 内部停止逻辑；wantLog 决定是否记录 GUI 日志。
func (g *gui) stopService(wantLog bool) {
	g.mu.Lock()
	srv := g.srv
	g.srv = nil
	g.mu.Unlock()
	if srv == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil && wantLog {
		g.logAdd(server.LogEntry{Time: time.Now(), Method: "GUI",
			Path: "停止超时，已强制断开剩余连接：" + err.Error()})
	}
	if wantLog {
		g.logAdd(server.LogEntry{Time: time.Now(), Method: "GUI", Path: "服务已停止"})
	}

	g.lastURL = nil
	g.addrBody.Objects = []fyne.CanvasObject{g.addrHint}
	g.addrBody.Refresh()
	g.stopBtn.Disable()
	g.openBtn.Disable()
	g.copyBtn.Disable()
	g.startBtn.Enable()
	for _, in := range g.inputs {
		in.Enable()
	}
}

// showAddr 运行态：地址卡主体切换为链接列表 + 指标行。
func (g *gui) showAddr(urls []string) {
	var segs []widget.RichTextSegment
	for i, u := range urls {
		if i > 0 {
			segs = append(segs, &widget.TextSegment{Text: "\n"})
		}
		if parsed, err := url.Parse(u); err == nil {
			segs = append(segs, &widget.HyperlinkSegment{Text: u, URL: parsed})
		}
	}
	g.addrRich.Segments = segs
	g.addrRich.Refresh()
	g.addrBody.Objects = []fyne.CanvasObject{
		g.addrRich,
		container.New(layout.NewCustomPaddedLayout(13, 0, 0, 0), g.metricRow),
	}
	g.addrBody.Refresh()
}

// ---------- 托盘 / 退出 / 动作 ----------

// setupTray 注册系统托盘图标与菜单（需在 ShowAndRun 前调用）。
// 桌面驱动实现了 desktop.App 接口，断言成功后才有托盘能力。
func (g *gui) setupTray() {
	show := fyne.NewMenuItem("显示主窗口", func() { g.win.Show() })
	quit := fyne.NewMenuItem("退出 LanShare", g.quit)
	menu := fyne.NewMenu("LanShare", show, fyne.NewMenuItemSeparator(), quit)
	if desk, ok := g.app.(desktop.App); ok {
		desk.SetSystemTrayMenu(menu)
	}
}

// quit 真正的退出：停止服务 → 退出应用（幂等，仅一次）。
func (g *gui) quit() {
	g.quitOnce.Do(func() {
		close(g.done)
		g.stopService(true)
		g.app.Quit()
	})
}

// openBrowser 用默认浏览器打开第一个访问地址。
func (g *gui) openBrowser() {
	if len(g.lastURL) == 0 {
		return
	}
	u, err := url.Parse(g.lastURL[0])
	if err != nil {
		return
	}
	_ = g.app.OpenURL(u)
}

// copyURL 复制全部访问地址到剪贴板，并临时提示。
func (g *gui) copyURL() {
	if len(g.lastURL) == 0 {
		return
	}
	g.win.Clipboard().SetContent(strings.Join(g.lastURL, "\n"))
	g.copyBtn.SetText("✓ 已复制全部")
	time.AfterFunc(2*time.Second, func() {
		g.postUI(func() { g.copyBtn.SetText("复制全部地址") })
	})
}

// postUI 把 UI 更新安全地投递到主协程；若已进入退出流程则直接丢弃。
func (g *gui) postUI(fn func()) {
	select {
	case <-g.done:
		return
	default:
		fyne.Do(fn)
	}
}

// errDialog 弹出错误提示。
func (g *gui) errDialog(msg string) {
	if msg == "" {
		return
	}
	dialog.ShowError(errors.New(msg), g.win)
}

// logFn 是 server 的日志回调（HTTP 处理协程调用，只入队不碰 UI）。
func (g *gui) logFn(e server.LogEntry) {
	g.logs.add(fmtLogEntry(e))
}

// logAdd 记录一条 GUI 本地日志。
func (g *gui) logAdd(e server.LogEntry) {
	g.logs.add(fmtLogEntry(e))
}

// ---------- 状态刷新 ----------

// updateLive 刷新状态胶囊与 4 项运行指标（幂等：文本未变则不重绘）。
func (g *gui) updateLive() {
	g.mu.Lock()
	srv := g.srv
	g.mu.Unlock()
	if srv == nil {
		g.chip.set(false, "未启动")
		for _, v := range g.metricVals {
			if v.Text != "—" {
				v.Text = "—"
				v.Color = colDim
				v.Refresh()
			}
		}
		return
	}
	g.chip.set(true, "运行中 · 端口 "+strconv.Itoa(srv.Port()))
	vals := [4]string{
		srv.Uptime().Round(time.Second).String(),
		strconv.FormatInt(srv.ActiveRequests(), 10),
		strconv.FormatInt(srv.ActiveDownloads(), 10),
		humanBytes(srv.BytesSent()),
	}
	for i, s := range vals {
		v := g.metricVals[i]
		if v.Text != s {
			v.Text = s
			v.Color = colInk
			v.Refresh()
		}
	}
}

// refreshNow 立即刷新一次状态栏与日志列表。
func (g *gui) refreshNow() {
	g.updateLive()
	if g.logs.takeDirty() {
		g.list.Refresh()
	}
}

// refreshLoop 周期刷新状态栏与日志列表（500ms）。
func (g *gui) refreshLoop() {
	tk := time.NewTicker(500 * time.Millisecond)
	defer tk.Stop()
	for {
		select {
		case <-g.done:
			return
		case <-tk.C:
			dirty := g.logs.takeDirty()
			g.postUI(func() {
				g.updateLive()
				if dirty {
					g.list.Refresh()
				}
			})
		}
	}
}

// ---------- 日志缓冲 ----------

type logBuffer struct {
	mu    sync.Mutex
	lines []string
	max   int
	dirty bool
}

func newLogBuffer(max int) *logBuffer {
	return &logBuffer{max: max}
}

// add 新日志插入头部（最新在顶，UI 无需滚动即可看到新记录）。
func (b *logBuffer) add(line string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.lines = append([]string{line}, b.lines...)
	if len(b.lines) > b.max {
		b.lines = b.lines[:b.max]
	}
	b.dirty = true
}

func (b *logBuffer) clear() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.lines = nil
	b.dirty = true
}

func (b *logBuffer) len() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.lines)
}

func (b *logBuffer) at(i int) string {
	b.mu.Lock()
	defer b.mu.Unlock()
	if i < 0 || i >= len(b.lines) {
		return ""
	}
	return b.lines[i]
}

func (b *logBuffer) takeDirty() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	d := b.dirty
	b.dirty = false
	return d
}

// ---------- 格式化 ----------

// fmtLogEntry 把一条访问日志格式化为单行文本（GUI 日志面板）。
func fmtLogEntry(e server.LogEntry) string {
	ts := e.Time.Format("15:04:05")
	p := string([]rune(e.Path))
	if len(p) > 160 {
		p = p[:160] + "…"
	}
	if e.Method == "SERVER" || e.Method == "GUI" {
		return fmt.Sprintf("%s  [%s] %s", ts, e.Method, p)
	}
	return fmt.Sprintf("%s  %-15s %-4s %s  →  %d  %s  %s",
		ts, e.Remote, e.Method, p, e.Status, humanBytes(e.Bytes), e.Dur.Round(time.Millisecond))
}

func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGTPE"[exp])
}

// splitIgnore 把逗号分隔的忽略规则文本拆成列表（去空白空项）。
func splitIgnore(s string) []string {
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	sort.Strings(out)
	return out
}
