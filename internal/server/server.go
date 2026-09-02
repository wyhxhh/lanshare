// Package server 实现 LanShare 的 HTTP 文件服务核心。
// 与 GUI 完全解耦：GUI（internal/gui）只负责装配 Config、调用 Start/Shutdown、
// 订阅日志事件与状态计数；本包可独立单元测试。
//
// 并发模型：net/http 每连接一个 goroutine；下载走 ServeContent 的零拷贝路径，
// 单机吞吐瓶颈在磁盘 IO 与网卡。不设 WriteTimeout（避免大文件被掐断）。
package server

import (
	"context"
	"errors"
	"fmt"
	"html/template"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/wyhxhh/lanshare/assets"
)

// LogEntry 一次 HTTP 请求的访问记录（GUI 日志面板 / headless stdout）。
type LogEntry struct {
	Time   time.Time
	Remote string // 客户端 IP:Port
	Method string
	Path   string // 相对共享根的显示路径（已解码）
	Status int
	Bytes  int64
	Dur    time.Duration
	UA     string
}

// Config 服务配置。
type Config struct {
	Root              string        // 共享根目录
	Port              int           // 监听端口，0 → 默认 8000
	Password          string        // 非空则启用访问密码
	ShowHidden        bool          // 是否显示/访问隐藏项（默认 false：过滤以 "." 开头与 Office 临时文件）
	Ignore            []string      // 忽略规则：匹配文件名或任一路径段（path.Match 语法，如 "*.tmp"）
	SessionTTL        time.Duration // 会话闲置超时（默认 30m）
	IdleTimeout       time.Duration
	ReadHeaderTimeout time.Duration
	LogFn             func(LogEntry) // 访问日志回调；nil 则输出到标准日志
}

const (
	DefaultPort              = 8000
	defaultSessionTTL        = 30 * time.Minute
	defaultIdleTimeout       = 90 * time.Second
	defaultReadHeaderTimeout = 10 * time.Second
	cookieName               = "lanshare_session"
)

func (c *Config) fillDefaults() {
	if c.Port == 0 {
		c.Port = DefaultPort
	}
	if c.SessionTTL <= 0 {
		c.SessionTTL = defaultSessionTTL
	}
	if c.IdleTimeout <= 0 {
		c.IdleTimeout = defaultIdleTimeout
	}
	if c.ReadHeaderTimeout <= 0 {
		c.ReadHeaderTimeout = defaultReadHeaderTimeout
	}
}

// Server HTTP 服务实例。零值不可用，须经 New 创建。
type Server struct {
	cfg      Config
	rootAbs  string // 规范化绝对路径
	rootReal string // EvalSymlinks 后的真实根（前缀校验基准）
	version  string

	mux *http.ServeMux
	srv *http.Server
	ln  net.Listener

	serveMu   sync.Mutex
	serveErr  error // Serve goroutine 的非正常退出错误
	startedAt time.Time

	activeReq atomic.Int64 // 处理中请求数
	activeDL  atomic.Int64 // 正在传输的下载/打包流数
	bytesOut  atomic.Int64 // 累计输出字节

	tplList  *template.Template
	tplLogin *template.Template

	authMu   sync.Mutex
	sessions map[string]time.Time // token → 过期时间
	failMu   sync.Mutex
	fails    map[string]failRec // IP → 失败记录（登录退避）
}

type failRec struct {
	count int
	until time.Time // 退避截止
}

// New 创建服务并完成所有校验；Root 不存在或模板加载失败时返回错误。
func New(cfg Config, version string) (*Server, error) {
	cfg.fillDefaults()
	abs, err := filepath.Abs(cfg.Root)
	if err != nil {
		return nil, fmt.Errorf("共享目录路径无效: %w", err)
	}
	info, err := os.Stat(abs)
	if err != nil {
		return nil, fmt.Errorf("共享目录不可访问: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("共享路径不是目录: %s", abs)
	}
	real, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return nil, fmt.Errorf("解析共享目录失败: %w", err)
	}

	s := &Server{
		cfg:      cfg,
		rootAbs:  abs,
		rootReal: real,
		version:  version,
		sessions: make(map[string]time.Time),
		fails:    make(map[string]failRec),
	}

	if err := s.loadTemplates(); err != nil {
		return nil, err
	}

	s.mux = http.NewServeMux()
	s.mux.HandleFunc("/", s.route)
	s.srv = &http.Server{
		Handler:           s,
		IdleTimeout:       cfg.IdleTimeout,
		ReadHeaderTimeout: cfg.ReadHeaderTimeout,
		// 有意不设 ReadTimeout/WriteTimeout：文件共享是慢速大流量场景。
	}
	return s, nil
}

func (s *Server) loadTemplates() error {
	listSrc, err := assets.Read("templates/list.html")
	if err != nil {
		return fmt.Errorf("加载目录页模板失败: %w", err)
	}
	loginSrc, err := assets.Read("templates/login.html")
	if err != nil {
		return fmt.Errorf("加载登录页模板失败: %w", err)
	}
	tl, err := template.New("list").Parse(string(listSrc))
	if err != nil {
		return fmt.Errorf("解析目录页模板失败: %w", err)
	}
	lg, err := template.New("login").Parse(string(loginSrc))
	if err != nil {
		return fmt.Errorf("解析登录页模板失败: %w", err)
	}
	s.tplList = tl
	s.tplLogin = lg
	return nil
}

// Root 返回规范化后的共享根路径。
func (s *Server) Root() string { return s.rootAbs }

// Config 返回当前配置（拷贝）。
func (s *Server) Config() Config { return s.cfg }

// Start 绑定端口并启动服务。端口被占用时返回友好错误。
// Port < 0 时绑定随机空闲端口（供测试/工具场景使用，实际端口用 Port() 查询）。
func (s *Server) Start() error {
	addr := ":0"
	if s.cfg.Port >= 0 {
		addr = fmt.Sprintf(":%d", s.cfg.Port)
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		if isAddrInUse(err) {
			return fmt.Errorf("端口 %d 已被占用（%v）。请更换端口或关闭占用程序后重试", s.cfg.Port, err)
		}
		return fmt.Errorf("监听 %s 失败: %w", addr, err)
	}
	s.ln = ln
	s.startedAt = time.Now()

	go func() {
		err := s.srv.Serve(ln)
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			s.serveMu.Lock()
			s.serveErr = err
			s.serveMu.Unlock()
			s.logf(LogEntry{Time: time.Now(), Method: "SERVER", Path: "监听中断: " + err.Error(), Status: 0})
		}
	}()
	s.logf(LogEntry{Time: time.Now(), Method: "SERVER", Path: fmt.Sprintf("服务已启动，共享目录: %s", s.rootAbs), Status: 0})
	return nil
}

// Err 返回 Serve goroutine 非正常退出的错误（正常 Shutdown 返回 nil）。
func (s *Server) Err() error {
	s.serveMu.Lock()
	defer s.serveMu.Unlock()
	return s.serveErr
}

// Addr 返回实际监听地址（如 [::]:8000）。
func (s *Server) Addr() string {
	if s.ln == nil {
		return ""
	}
	return s.ln.Addr().String()
}

// Port 返回实际端口。
func (s *Server) Port() int {
	if s.ln == nil {
		return s.cfg.Port
	}
	if ta, ok := s.ln.Addr().(*net.TCPAddr); ok {
		return ta.Port
	}
	return s.cfg.Port
}

// Shutdown 优雅关闭：停止接收新连接并等待活跃请求完成；
// 超过 ctx 宽限则强制断开。
func (s *Server) Shutdown(ctx context.Context) error {
	s.logf(LogEntry{Time: time.Now(), Method: "SERVER", Path: "正在停止服务…", Status: 0})
	if s.srv == nil {
		return nil
	}
	return s.srv.Shutdown(ctx)
}

// ActiveRequests / ActiveDownloads / BytesSent 供 GUI 状态栏实时展示。
func (s *Server) ActiveRequests() int64  { return s.activeReq.Load() }
func (s *Server) ActiveDownloads() int64 { return s.activeDL.Load() }
func (s *Server) BytesSent() int64       { return s.bytesOut.Load() }

// Uptime 返回服务启动至今时长。
func (s *Server) Uptime() time.Duration {
	if s.startedAt.IsZero() {
		return 0
	}
	return time.Since(s.startedAt)
}

func (s *Server) logf(e LogEntry) {
	if s.cfg.LogFn != nil {
		s.cfg.LogFn(e)
		return
	}
	// headless 兜底：标准日志
	if e.Method == "SERVER" {
		log.Printf("[server] %s", e.Path)
	} else {
		log.Printf("%s %s %s %d %dB %s", e.Remote, e.Method, e.Path, e.Status, e.Bytes, e.Dur.Round(time.Millisecond))
	}
}

// ServeHTTP 统一入口：包装统计中间件后分发到内部路由。
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	s.activeReq.Add(1)
	defer s.activeReq.Add(-1)

	sw := &statWriter{ResponseWriter: w}
	s.mux.ServeHTTP(sw, r)

	// 累计输出字节（GUI 状态栏"累计发送"口径，含所有响应）
	s.bytesOut.Add(sw.written())

	// 访问日志（仅记录可读路径，/healthz 除外）
	if !strings.HasPrefix(r.URL.Path, "/healthz") {
		s.logf(LogEntry{
			Time:   time.Now(),
			Remote: clientIP(r),
			Method: r.Method,
			Path:   r.URL.RequestURI(),
			Status: sw.statusCode(),
			Bytes:  sw.written(),
			Dur:    time.Since(start),
			UA:     r.UserAgent(),
		})
	}
}

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// statWriter 包装 ResponseWriter：统计响应字节与状态码，
// 并保真 Flusher/Hijacker/ReaderFrom（ReaderFrom 保证大文件零拷贝路径不受影响）。
type statWriter struct {
	http.ResponseWriter
	n    int64
	code int
}

func (w *statWriter) Write(p []byte) (int, error) {
	n, err := w.ResponseWriter.Write(p)
	w.n += int64(n)
	return n, err
}

func (w *statWriter) WriteHeader(code int) {
	if w.code == 0 {
		w.code = code
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *statWriter) statusCode() int {
	if w.code == 0 {
		return http.StatusOK
	}
	return w.code
}

func (w *statWriter) written() int64 { return w.n }

func (w *statWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// ReadFrom 转发到底层：若底层支持 ReaderFrom（sendfile/零拷贝），
// 走底层实现并累计字节；否则退化为 io.Copy 到原始 ResponseWriter。
func (w *statWriter) ReadFrom(r io.Reader) (int64, error) {
	var n int64
	var err error
	if rf, ok := w.ResponseWriter.(io.ReaderFrom); ok {
		n, err = rf.ReadFrom(r)
	} else {
		n, err = io.Copy(w.ResponseWriter, r)
	}
	w.n += n
	if w.code == 0 {
		w.code = http.StatusOK
	}
	return n, err
}

func isAddrInUse(err error) bool {
	var ne *net.OpError
	return errors.As(err, &ne) && (strings.Contains(ne.Err.Error(), "address already in use") ||
		strings.Contains(ne.Err.Error(), "仅允许使用一个") ||
		strings.Contains(ne.Err.Error(), "Only one usage"))
}
