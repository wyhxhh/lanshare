package server

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// ---------------- 数据模型 ----------------

// dirEntry 目录中的一个条目（内部用，排序后转渲染视图）。
type dirEntry struct {
	Name  string
	IsDir bool
	Size  int64
	Mod   time.Time
}

type crumb struct {
	Name string // 显示名
	Rel  string // 相对路径（"" = 根）
	URL  string // 面包屑链接（"" = 当前不可点）
}

type viewEntry struct {
	Name   string
	IsDir  bool
	Size   string // 人类可读或 "—"
	Mod    string // YYYY-MM-DD HH:MM
	URL    string // 下载 /dl 或浏览 /?path=
	IsZip  bool   // 目录的 ZIP 下载链接
	ZipURL string
}

type paging struct {
	Page    int
	HasPrev bool
	HasNext bool
	PrevURL string
	NextURL string
}

const pageSize = 500

// ---------------- 路由分发 ----------------

// route 是唯一入口（注册在 "/"），按路径分发给各业务 handler。
// 除 /login /logout 外均先过 requireAuth。
func (s *Server) route(w http.ResponseWriter, r *http.Request) {
	p := r.URL.Path
	switch p {
	case "/healthz":
		s.handleHealth(w, r)
	case "/login":
		s.handleLogin(w, r)
	case "/logout":
		s.handleLogout(w, r)
	case "/dl":
		s.requireAuth(s.handleDownload)(w, r)
	case "/zip":
		s.requireAuth(s.handleZip)(w, r)
	case "/", "/index.html":
		s.requireAuth(s.handleList)(w, r)
	default:
		http.NotFound(w, r)
	}
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	io.WriteString(w, "ok")
}

// ---------------- 目录浏览 ----------------

// handleList 渲染目录索引页（GET/HEAD），支持 ?path=相对目录 与 ?page=N。
func (s *Server) handleList(w http.ResponseWriter, r *http.Request) {
	rel := r.URL.Query().Get("path")

	abs, err := s.resolveSafe(rel)
	if err != nil {
		if os.IsNotExist(err) {
			s.renderError(w, "路径不存在", err, http.StatusNotFound)
		} else {
			s.renderError(w, "无法访问该路径", err, http.StatusBadRequest)
		}
		return
	}
	info, err := os.Stat(abs)
	if err != nil {
		if os.IsNotExist(err) {
			s.renderError(w, "路径不存在", err, http.StatusNotFound)
		} else {
			s.renderError(w, "无法读取该路径", err, http.StatusInternalServerError)
		}
		return
	}
	if !info.IsDir() {
		// 根路径指向文件：转下载
		http.Redirect(w, r, "/dl?path="+url.QueryEscape(rel), http.StatusFound)
		return
	}

	cleaned := rel
	if cl, err := cleanSegments(rel, s.cfg.ShowHidden); err == nil {
		cleaned = cl
	}

	entries, err := s.listVisibleEntries(abs)
	if err != nil {
		s.renderError(w, "读取目录失败", err, http.StatusInternalServerError)
		return
	}

	// 分页（自然排序后切片）
	page := 1
	if ps := r.URL.Query().Get("page"); ps != "" {
		if n, err := strconv.Atoi(ps); err == nil && n > 0 {
			page = n
		}
	}
	totalPages := (len(entries) + pageSize - 1) / pageSize
	if totalPages == 0 {
		totalPages = 1
	}
	if page > totalPages {
		page = totalPages
	}
	lo, hi := (page-1)*pageSize, page*pageSize
	if lo > len(entries) {
		lo = len(entries)
	}
	if hi > len(entries) {
		hi = len(entries)
	}
	pageEntries := entries[lo:hi]

	// 组装面包屑
	crumbs := []crumb{{Name: "根目录", Rel: "", URL: "/"}}
	if cleaned != "" {
		segs := strings.Split(cleaned, "/")
		acc := ""
		for _, seg := range segs {
			if acc == "" {
				acc = seg
			} else {
				acc += "/" + seg
			}
			crumbs = append(crumbs, crumb{Name: seg, Rel: acc, URL: "/?path=" + url.QueryEscape(acc)})
		}
	}
	parentRel := ""
	if cleaned != "" {
		idx := strings.LastIndex(cleaned, "/")
		if idx < 0 {
			parentRel = ""
		} else {
			parentRel = cleaned[:idx]
		}
	}

	// 渲染视图
	views := make([]viewEntry, 0, len(pageEntries))
	for _, e := range pageEntries {
		eRel := e.Name
		if cleaned != "" {
			eRel = cleaned + "/" + e.Name
		}
		ve := viewEntry{
			Name:  e.Name,
			IsDir: e.IsDir,
			Mod:   e.Mod.Format("2006-01-02 15:04"),
		}
		if e.IsDir {
			ve.Size = "—"
			ve.URL = "/?path=" + url.QueryEscape(eRel)
			ve.IsZip = true
			ve.ZipURL = "/zip?paths=" + url.QueryEscape(eRel)
		} else {
			ve.Size = humanSize(e.Size)
			ve.URL = "/dl?path=" + url.QueryEscape(eRel)
		}
		views = append(views, ve)
	}

	pg := paging{Page: page, HasPrev: page > 1, HasNext: page < totalPages}
	q := url.Values{}
	if cleaned != "" {
		q.Set("path", cleaned)
	}
	if pg.HasPrev {
		q.Set("page", strconv.Itoa(page-1))
		pg.PrevURL = "/?" + q.Encode()
	}
	if pg.HasNext {
		q.Set("page", strconv.Itoa(page+1))
		pg.NextURL = "/?" + q.Encode()
	}

	// 全目录 ZIP（当前目录，含根）
	allZipURL := "/zip?paths=" + url.QueryEscape(cleaned)

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	data := struct {
		Title     string
		RootName  string
		Crumbs    []crumb
		ParentRel string
		Entries   []viewEntry
		Empty     bool
		Paging    paging
		AllZipURL string
		ZipName   string
		Version   string
		AuthOn    bool
		LogoutURL string
	}{
		Title:     s.pageTitle(cleaned),
		RootName:  filepath.Base(s.rootAbs),
		Crumbs:    crumbs,
		ParentRel: parentRel,
		Entries:   views,
		Empty:     len(views) == 0,
		Paging:    pg,
		AllZipURL: allZipURL,
		ZipName:   s.zipDisplayName(cleaned),
		Version:   s.version,
		AuthOn:    s.passwordEnabled(),
		LogoutURL: "/logout",
	}
	if err := s.tplList.Execute(w, data); err != nil {
		http.Error(w, "渲染页面失败", http.StatusInternalServerError)
	}
}

func (s *Server) pageTitle(cleaned string) string {
	if cleaned == "" {
		return "LanShare 文件共享"
	}
	segs := strings.Split(cleaned, "/")
	return segs[len(segs)-1] + " - LanShare"
}

// zipDisplayName 供 zip 文件名与页面展示。
func (s *Server) zipDisplayName(rel string) string {
	if rel == "" {
		return filepath.Base(s.rootAbs)
	}
	return filepath.Base(rel)
}

// ---------------- 下载（Range / HEAD） ----------------

// inlineExts 浏览器内联预览的类型白名单；其余一律附件下载。
var inlineExts = map[string]bool{
	".txt": true, ".md": true, ".log": true, ".json": true, ".csv": true,
	".pdf": true,
	".png": true, ".jpg": true, ".jpeg": true, ".gif": true, ".webp": true, ".svg": true, ".bmp": true, ".ico": true,
	".mp3": true, ".wav": true, ".flac": true, ".ogg": true,
	".mp4": true, ".webm": true, ".mov": true, ".mkv": true, ".avi": true,
}

// handleDownload 提供单文件下载/预览（支持 Range 断点续传与 HEAD）。
func (s *Server) handleDownload(w http.ResponseWriter, r *http.Request) {
	rel := r.URL.Query().Get("path")
	if rel == "" {
		s.renderError(w, "缺少文件路径", nil, http.StatusBadRequest)
		return
	}
	abs, err := s.resolveSafe(rel)
	if err != nil {
		if os.IsNotExist(err) {
			s.renderError(w, "文件不存在", err, http.StatusNotFound)
		} else {
			s.renderError(w, "无法访问该文件", err, http.StatusForbidden)
		}
		return
	}
	f, err := os.Open(abs)
	if err != nil {
		if os.IsNotExist(err) {
			s.renderError(w, "文件不存在", err, http.StatusNotFound)
		} else {
			s.renderError(w, "无法打开文件", err, http.StatusForbidden)
		}
		return
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		s.renderError(w, "无法读取文件信息", err, http.StatusInternalServerError)
		return
	}
	if info.IsDir() {
		// 下载目录 → 转 ZIP 打包
		http.Redirect(w, r, "/zip?paths="+url.QueryEscape(rel), http.StatusFound)
		return
	}

	name := filepath.Base(abs)
	s.activeDL.Add(1)
	defer s.activeDL.Add(-1)

	// 附件/内联策略 + RFC 5987 中文文件名
	ext := strings.ToLower(filepath.Ext(name))
	if !inlineExts[ext] {
		w.Header().Set("Content-Disposition", contentDisposition(name))
	}
	http.ServeContent(w, r, name, info.ModTime(), f)
}

// contentDisposition 生成兼容中文/特殊字符的附件头：
// 老客户端回退 ASCII，现代客户端用 filename*=UTF-8”。
func contentDisposition(name string) string {
	fallback := make([]rune, 0, len(name))
	for _, c := range name {
		if c < 0x20 || c > 0x7e || c == '"' || c == '\\' {
			fallback = append(fallback, '_')
		} else {
			fallback = append(fallback, c)
		}
	}
	return fmt.Sprintf("attachment; filename=\"%s\"; filename*=UTF-8''%s",
		string(fallback), url.PathEscape(name))
}

// ---------------- 错误页 ----------------

func (s *Server) renderError(w http.ResponseWriter, msg string, err error, code int) {
	if err != nil {
		msg = msg + "：" + err.Error()
	}
	http.Error(w, msg, code)
}

// ---------------- 工具函数 ----------------

func humanSize(n int64) string {
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

// naturalLess 自然排序：数字段按数值比较（file2 < file10）。
func naturalLess(a, b string) bool {
	for {
		// 定位首个数字段
		ia := firstDigit(a)
		ib := firstDigit(b)
		if ia < 0 && ib < 0 {
			return a < b
		}
		if ia < 0 {
			return true // a 无更多数字段 → 更小
		}
		if ib < 0 {
			return false
		}
		pa := a[:ia]
		pb := b[:ib]
		if pa != pb {
			return pa < pb
		}
		// 数字段比较
		ja := ia
		for ja < len(a) && a[ja] >= '0' && a[ja] <= '9' {
			ja++
		}
		jb := ib
		for jb < len(b) && b[jb] >= '0' && b[jb] <= '9' {
			jb++
		}
		na, nb := a[ia:ja], b[ib:jb]
		// 去前导零比较长度
		ta, tb := strings.TrimLeft(na, "0"), strings.TrimLeft(nb, "0")
		if len(ta) != len(tb) {
			return len(ta) < len(tb)
		}
		if ta != tb {
			return ta < tb
		}
		a, b = a[ja:], b[jb:]
	}
}

func firstDigit(s string) int {
	for i := 0; i < len(s); i++ {
		if s[i] >= '0' && s[i] <= '9' {
			return i
		}
	}
	return -1
}

func sortDirEntries(es []dirEntry) {
	sort.SliceStable(es, func(i, j int) bool {
		if es[i].IsDir != es[j].IsDir {
			return es[i].IsDir // 目录在前
		}
		return naturalLess(es[i].Name, es[j].Name)
	})
}
