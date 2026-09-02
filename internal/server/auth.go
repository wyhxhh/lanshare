package server

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// auth 相关路由：/login（GET 表单 / POST 校验）、/logout。
// 未开启密码时全部直接放行。

const (
	loginFailBaseDelay = 300 * time.Millisecond
	loginFailMaxDelay  = 5 * time.Second
)

// passwordEnabled 当前是否启用访问密码。
func (s *Server) passwordEnabled() bool { return s.cfg.Password != "" }

// requireAuth 中间件：未启用密码或会话有效则放行；
// 否则未登录请求重定向到 /login（保留 next 以便回跳）。
func (s *Server) requireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.passwordEnabled() {
			next(w, r)
			return
		}
		if s.validSession(r) {
			next(w, r)
			return
		}
		// 静态资源/健康检查不拦（登录页本身所需）
		if r.URL.Path == "/login" {
			next(w, r)
			return
		}
		http.Redirect(w, r, "/login?next="+queryEscapeRel(r.URL.RequestURI()), http.StatusFound)
	}
}

// validSession 校验请求携带的会话 cookie 且未过期。
func (s *Server) validSession(r *http.Request) bool {
	c, err := r.Cookie(cookieName)
	if err != nil || c.Value == "" {
		return false
	}
	s.authMu.Lock()
	defer s.authMu.Unlock()
	exp, ok := s.sessions[c.Value]
	if !ok {
		return false
	}
	if time.Now().After(exp) {
		delete(s.sessions, c.Value)
		return false
	}
	// 滑动续期
	s.sessions[c.Value] = time.Now().Add(s.cfg.SessionTTL)
	return true
}

// handleLogin GET 渲染登录页；POST 校验密码并种会话 cookie。
func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	if !s.passwordEnabled() {
		http.Redirect(w, r, "/", http.StatusFound)
		return
	}
	// 已登录直接回跳
	if s.validSession(r) {
		http.Redirect(w, r, safeNext(r), http.StatusFound)
		return
	}

	if r.Method == http.MethodPost {
		if s.loginThrottled(r) {
			http.Error(w, "尝试过于频繁，请稍后再试", http.StatusTooManyRequests)
			return
		}
		r.ParseForm()
		got := r.Form.Get("password")
		want := s.cfg.Password
		// 常数时间比较，避免时序侧信道
		ok := len(got) == len(want) && subtle.ConstantTimeCompare([]byte(got), []byte(want)) == 1
		if !ok {
			s.recordLoginFail(r, false)
			s.renderLogin(w, r, "密码错误，请重试")
			return
		}
		s.recordLoginFail(r, true)
		s.issueSession(w)
		http.Redirect(w, r, safeNext(r), http.StatusFound)
		return
	}

	s.renderLogin(w, r, "")
}

func (s *Server) renderLogin(w http.ResponseWriter, r *http.Request, errMsg string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	data := map[string]string{"Error": errMsg, "Next": safeNext(r)}
	if err := s.tplLogin.Execute(w, data); err != nil {
		http.Error(w, "渲染登录页失败", http.StatusInternalServerError)
	}
}

// handleLogout 清除会话。
func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(cookieName); err == nil {
		s.authMu.Lock()
		delete(s.sessions, c.Value)
		s.authMu.Unlock()
	}
	http.SetCookie(w, &http.Cookie{
		Name: cookieName, Value: "", Path: "/",
		HttpOnly: true, SameSite: http.SameSiteLaxMode,
		MaxAge: -1,
	})
	http.Redirect(w, r, "/", http.StatusFound)
}

// issueSession 生成随机会话 token 并种 cookie。
func (s *Server) issueSession(w http.ResponseWriter) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		http.Error(w, "服务器内部错误", http.StatusInternalServerError)
		return
	}
	token := hex.EncodeToString(buf)
	s.authMu.Lock()
	s.sessions[token] = time.Now().Add(s.cfg.SessionTTL)
	s.authMu.Unlock()
	http.SetCookie(w, &http.Cookie{
		Name:     cookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(s.cfg.SessionTTL.Seconds()),
	})
}

// safeNext 仅允许站内相对路径作为回跳目标，防止开放重定向。
// 兼容两种携带方式：GET /login?next=… 与登录表单隐藏域（POST 回跳）。
func safeNext(r *http.Request) string {
	next := r.URL.Query().Get("next")
	if next == "" {
		next = r.FormValue("next")
	}
	if next == "" || !strings.HasPrefix(next, "/") ||
		strings.HasPrefix(next, "//") || strings.HasPrefix(next, "/\\") {
		return "/"
	}
	return next
}

// loginThrottled 按客户端 IP 判断是否处于退避期。
func (s *Server) loginThrottled(r *http.Request) bool {
	ip := clientIP(r)
	s.failMu.Lock()
	defer s.failMu.Unlock()
	rec, ok := s.fails[ip]
	return ok && time.Now().Before(rec.until)
}

// recordLoginFail 记录失败（指数退避）；成功则清零。
func (s *Server) recordLoginFail(r *http.Request, success bool) {
	ip := clientIP(r)
	s.failMu.Lock()
	defer s.failMu.Unlock()
	if success {
		delete(s.fails, ip)
		return
	}
	rec := s.fails[ip]
	rec.count++
	delay := loginFailBaseDelay << uint(min(rec.count-1, 4))
	if delay > loginFailMaxDelay {
		delay = loginFailMaxDelay
	}
	rec.until = time.Now().Add(delay)
	s.fails[ip] = rec
}

// queryEscapeRel 对回跳 URL 做标准查询参数编码（仅路径，不加 host）。
func queryEscapeRel(u string) string {
	return url.QueryEscape(u)
}
