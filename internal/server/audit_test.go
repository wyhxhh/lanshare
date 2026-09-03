package server

import (
	"context"
	crand "crypto/rand"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// 本文件为代码审查期加入的"问题坐实"测试：每条对应一处可疑实现，
// 用可复现的断言把问题钉死（修复后这些断言应转为通过）。
//
// 注意：涉及"响应头是否在首次写入前设置"的验证必须走真实 TCP 连接，
// httptest.NewRecorder 不会冻结 header，用它测会假阴性（测不出问题）。

// quiet 关闭测试期访问日志（避免刷屏，不影响被测行为）。
func quiet(c *Config) { c.LogFn = func(LogEntry) {} }

// ---------- 1. 内联预览的安全响应头 ----------

// TestAuditInlineSVGSecurityHeaders 校验内联预览（不触发下载的类型）是否带上
// CSP / nosniff。浏览器会直接渲染 SVG 并执行其中脚本，缺少这些头意味着
// 同源 XSS：恶意 svg 可读取同域下的任意文件（含带密码保护的内容）。
func TestAuditInlineSVGSecurityHeaders(t *testing.T) {
	srv, base := startTestServer(t, quiet)
	svg := `<svg xmlns="http://www.w3.org/2000/svg"><script>fetch('/dl?path=hello.txt')</script></svg>`
	if err := os.WriteFile(filepath.Join(srv.Root(), "evil.svg"), []byte(svg), 0o644); err != nil {
		t.Fatal(err)
	}

	resp := rawGet(t, base, `/dl?path=evil.svg`, nil)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("前置条件失败：svg 未返回 200，实际 %d", resp.StatusCode)
	}
	if disp := resp.Header.Get("Content-Disposition"); disp != "" {
		t.Fatalf("前置条件失败：svg 走了附件下载，无法验证内联路径")
	}
	csp := resp.Header.Get("Content-Security-Policy")
	if !strings.Contains(csp, "sandbox") {
		t.Errorf("SVG 内联预览缺少 CSP sandbox：Content-Security-Policy=%q\n"+
			"  Content-Type=%s\n  → 浏览器会执行 SVG 内嵌脚本，构成同源 XSS"+
			"（可读取同域下任意共享文件，访问密码一并失效）",
			csp, resp.Header.Get("Content-Type"))
	}
	if got := resp.Header.Get("X-Content-Type-Options"); got != "nosniff" {
		t.Errorf("缺少 X-Content-Type-Options: nosniff（实际 %q）", got)
	}
}

// ---------- 2. ZIP 失败报告头的可达性 ----------

// TestAuditZipFailedHeaderDelivered 校验打包失败时 X-LanShare-ZipFailed 是否
// 真的送达客户端。响应头必须在首次写入响应体之前设置，而失败项数量要等打包
// 结束才知道 —— 若实现顺序不对，这个头永远不会生效。
func TestAuditZipFailedHeaderDelivered(t *testing.T) {
	srv, base := startTestServer(t, quiet)

	// 共享根外建一个目标目录，再在共享根内用符号链接指向它（打包时应被跳过）
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("outside"), 0o644); err != nil {
		t.Fatal(err)
	}
	esc := filepath.Join(srv.Root(), "esc")
	if err := os.MkdirAll(esc, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(esc, "ok.txt"), []byte("ok"), 0o644); err != nil {
		t.Fatal(err)
	}
	// 关键：放一个足够大的不可压缩文件，使 zip 输出远超 Go http 的响应缓冲区
	// （默认 4KB）。缓冲区内的小响应会被延迟到 handler 返回才发送 —— 那会掩盖
	// "响应头设置过晚"的问题。真实打包几乎都会超过 4KB，必须按真实体量测。
	// 用真随机数据保证 deflate 压不动，输出必然远超 4KB
	big := make([]byte, 128*1024)
	if _, err := crand.Read(big); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(esc, "big.bin"), big, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "secret.txt"), filepath.Join(esc, "sneak.txt")); err != nil {
		t.Skipf("当前环境无法创建符号链接，跳过：%v", err)
	}

	resp := rawGet(t, base, "/zip?paths=esc", nil)
	defer resp.Body.Close()
	n, _ := io.Copy(io.Discard, resp.Body)
	if n < 4096 {
		t.Fatalf("前置条件失败：zip 仅 %d 字节，未超过响应缓冲区，测不出问题", n)
	}

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("前置条件失败：/zip 返回 %d", resp.StatusCode)
	}
	// 失败计数走 HTTP Trailer：必须读完 body 才能取到
	if got := resp.Trailer.Get(zipFailedHeader); got == "" {
		t.Errorf("%s 未送达客户端（响应 %d 字节，打包确有失败项，期望值为 1）\n"+
			"  → 该计数只能在响应体写出后确定，用响应头传递会在超过缓冲区时丢失",
			zipFailedHeader, n)
	}
}

// ---------- 3. 会话表的过期回收 ----------

// TestAuditSessionMapReaped 校验过期会话是否会被回收。若不回收，长期运行
// （多人反复登录）会让 sessions 单调增长，属于慢速内存泄漏。
func TestAuditSessionMapReaped(t *testing.T) {
	old := reapInterval
	reapInterval = 40 * time.Millisecond
	defer func() { reapInterval = old }()

	cfg := Config{Root: t.TempDir(), Password: "pw", SessionTTL: 60 * time.Millisecond, Port: -1}
	quiet(&cfg)
	srv, err := New(cfg, "test")
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.Start(); err != nil { // 启动后回收协程才开始工作
		t.Fatal(err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
	}()

	const n = 25
	for i := 0; i < n; i++ {
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/login",
			strings.NewReader("password=pw&next=/"))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		srv.ServeHTTP(rr, req)
		if rr.Result().StatusCode != http.StatusFound {
			t.Fatalf("第 %d 次登录失败，状态码 %d", i, rr.Result().StatusCode)
		}
	}
	srv.authMu.Lock()
	before := len(srv.sessions)
	srv.authMu.Unlock()
	if before != n {
		t.Fatalf("前置条件失败：期望 %d 个会话，实际 %d", n, before)
	}

	time.Sleep(300 * time.Millisecond) // 等会话过期 + 回收周期走一轮

	srv.authMu.Lock()
	after := len(srv.sessions)
	srv.authMu.Unlock()
	if after != 0 {
		t.Errorf("过期会话未被回收：TTL 到期后仍有 %d/%d 条残留在 sessions 中\n"+
			"  → 会话只在被再次访问时懒删除，无人访问的 token 会永久驻留", after, before)
	}
}

// ---------- 4. 登录失败记录表的上限 ----------

// TestAuditFailMapBounded 校验失败登录记录是否会无界增长。
// 每个来源 IP 占一条，扫描/探测场景下持续累积。
func TestAuditFailMapBounded(t *testing.T) {
	oldReap, oldRetain := reapInterval, failRetainAfter
	reapInterval, failRetainAfter = 40*time.Millisecond, 10*time.Millisecond
	defer func() { reapInterval, failRetainAfter = oldReap, oldRetain }()

	cfg := Config{Root: t.TempDir(), Password: "pw", Port: -1}
	quiet(&cfg)
	srv, err := New(cfg, "test")
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
	}()

	const n = 300
	for i := 0; i < n; i++ {
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/login",
			strings.NewReader("password=wrong"))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.RemoteAddr = fmt.Sprintf("10.1.%d.%d:5555", i/250, i%250+1)
		srv.ServeHTTP(rr, req)
	}
	srv.failMu.Lock()
	peak := len(srv.fails)
	srv.failMu.Unlock()
	if peak < n/2 {
		t.Fatalf("前置条件失败：期望累积大量失败记录，实际仅 %d", peak)
	}

	// 首次失败退避固定 300ms（loginFailBaseDelay），叠加保留期与回收周期
	// → 需等足 300ms + failRetainAfter + reapInterval 才会被回收
	time.Sleep(700 * time.Millisecond)

	srv.failMu.Lock()
	got := len(srv.fails)
	srv.failMu.Unlock()
	if got > 256 {
		t.Errorf("失败记录表未回收：%d 次失败后峰值 %d 条，回收后仍有 %d 条\n"+
			"  → 记录只在对应 IP 登录成功时清除，探测式扫描可持续推高内存",
			n, peak, got)
	}
}

// ---------- 5. 畸形路径的状态码 ----------

// TestAuditMalformedPathStatus 校验畸形路径返回的状态码。
// 客户端错误应落在 4xx，返回 5xx 会污染"服务端故障"语义。
func TestAuditMalformedPathStatus(t *testing.T) {
	_, base := startTestServer(t, quiet)
	cases := map[string]string{
		"NUL 字节": "/?path=a%00b",
		"超长单段":   "/?path=" + strings.Repeat("d", 300),
	}
	for name, target := range cases {
		code := statusOf(t, base, target)
		if code >= 500 {
			t.Errorf("%s：返回 %d（应为 4xx —— 这是客户端畸形输入，不是服务端故障）", name, code)
		}
	}
}

// ---------- 6. statWriter 的接口保真度 ----------

// TestAuditStatWriterHijacker 校验 statWriter 是否保真了注释声称的接口。
// 缺失 Hijacker 会让底层能力在包装后丢失（例如未来的 WebSocket/协议升级）。
func TestAuditStatWriterHijacker(t *testing.T) {
	var w http.ResponseWriter = &statWriter{}
	if _, ok := w.(http.Hijacker); !ok {
		t.Errorf("statWriter 未实现 http.Hijacker，与其注释声明的" +
			"「保真 Flusher/Hijacker/ReaderFrom」不符\n" +
			"  → 包装会静默吞掉底层能力，且编译器不会报错")
	}
	if _, ok := w.(http.Flusher); !ok {
		t.Errorf("statWriter 未实现 http.Flusher")
	}
	if _, ok := w.(io.ReaderFrom); !ok {
		t.Errorf("statWriter 未实现 io.ReaderFrom（大文件会退化成用户态拷贝）")
	}
}

// ---------- 9. 全站安全基线头 ----------

// TestAuditSecurityBaselineHeaders 校验所有响应（含错误页与健康检查）都带上
// 安全基线头。少了它们，文本类文件可能被浏览器嗅探成网页执行，
// 页面也可能被第三方 iframe 嵌套做点击劫持。
func TestAuditSecurityBaselineHeaders(t *testing.T) {
	_, base := startTestServer(t, quiet)
	for _, path := range []string{"/", "/healthz", "/?path=definitely-not-exist", "/dl?path=hello.txt"} {
		resp := rawGet(t, base, path, nil)
		_, _ = io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		if got := resp.Header.Get("X-Content-Type-Options"); got != "nosniff" {
			t.Errorf("%s：缺少 X-Content-Type-Options: nosniff（实际 %q）", path, got)
		}
		if got := resp.Header.Get("X-Frame-Options"); got == "" {
			t.Errorf("%s：缺少 X-Frame-Options（可被第三方页面 iframe 嵌套）", path)
		}
	}
}

// ---------- 10. 会话校验的并发开销 ----------

// BenchmarkValidSession 量化会话校验在并发下的开销。
// 若每个请求都要抢全局写锁做滑动续期，这里会随并发数上升而明显变慢。
func BenchmarkValidSession(b *testing.B) {
	cfg := Config{Root: b.TempDir(), Password: "pw", SessionTTL: time.Hour, Port: -1}
	cfg.LogFn = func(LogEntry) {}
	srv, err := New(cfg, "bench")
	if err != nil {
		b.Fatal(err)
	}
	// 预置一批有效会话，模拟多设备同时在线
	const nTok = 64
	toks := make([]string, nTok)
	for i := range toks {
		toks[i] = "token-" + strconv.Itoa(i)
		srv.sessions[toks[i]] = time.Now().Add(time.Hour)
	}

	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		// 请求对象复用：否则 httptest.NewRequest 的分配开销会淹没被测的锁路径
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		i := 0
		for pb.Next() {
			req.Header.Set("Cookie", cookieName+"="+toks[i%nTok])
			if !srv.validSession(req) {
				b.Fatal("有效会话被判为无效")
			}
			i++
		}
	})
}

// ---------- 7. 下载目录的重定向 ----------

// TestAuditDownloadDirRedirectsToZip 校验 /dl 指向目录时是否正确改道打包下载。
// Windows 上打开目录句柄需要特殊标志，此处验证改道链路未被该差异打断。
func TestAuditDownloadDirRedirectsToZip(t *testing.T) {
	_, base := startTestServer(t, quiet)
	resp := rawGet(t, base, "/dl?path=sub", nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusFound {
		t.Errorf("/dl 指向目录时返回 %d（期望 302 改道到 /zip）", resp.StatusCode)
		return
	}
	loc := resp.Header.Get("Location")
	if !strings.HasPrefix(loc, "/zip?paths=sub") {
		t.Errorf("重定向目标异常：%q（期望 /zip?paths=sub…）", loc)
	}
}

// ---------- 8. 关闭后不应残留 goroutine ----------

// TestAuditShutdownReleasesPort 校验 Shutdown 后监听端口被释放（listener 未泄漏）。
func TestAuditShutdownReleasesPort(t *testing.T) {
	srv, _ := startTestServer(t, quiet)
	port := srv.Port()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	// 关闭后端口应能立即被复用（否则说明 listener 未释放）
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		t.Errorf("Shutdown 后端口 %d 仍被占用：%v（listener 未释放）", port, err)
	} else {
		_ = ln.Close()
	}
}
