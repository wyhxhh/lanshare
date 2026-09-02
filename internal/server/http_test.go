package server

import (
	"archive/zip"
	"bytes"
	"context"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// ---------- 测试基础设施 ----------

// startTestServer 建临时共享根、写入标准测试文件，并启动真实 HTTP 服务（随机端口）。
func startTestServer(t *testing.T, mutate func(*Config)) (*Server, string) {
	t.Helper()
	root := t.TempDir()
	write := func(rel, data string) {
		t.Helper()
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("hello.txt", "Hello, LanShare!\n")
	write("report 2026.pdf", "PDF-BINARY-0123456789ABCDEF")
	write("sub/data.bin", bin255())
	write("sub/中文文件.txt", "中文内容")
	write("junk.tmp", "should be ignored by rule")
	write(".hidden.txt", "secret-hidden")
	write("~$tmp.docx", "office temp")

	cfg := Config{Root: root, Port: -1}
	if mutate != nil {
		mutate(&cfg)
	}
	srv, err := New(cfg, "test")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := srv.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
	})
	base := "http://127.0.0.1:" + strconv.Itoa(srv.Port())
	return srv, base
}

func bin255() string {
	var b strings.Builder
	for i := 0; i < 256; i++ {
		b.WriteByte(byte(i))
	}
	return b.String()
}

// rawGet 不跟随重定向发起请求，返回 resp（调用方负责 Close）。
func rawGet(t *testing.T, base, target string, hdr map[string]string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, base+target, nil)
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", target, err)
	}
	return resp
}

func bodyString(t *testing.T, resp *http.Response) string {
	t.Helper()
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func statusOf(t *testing.T, base, target string) int {
	t.Helper()
	resp := rawGet(t, base, target, nil)
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	return resp.StatusCode
}

func keys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// ---------- 目录列表 ----------

func TestListRootAndSubdir(t *testing.T) {
	_, base := startTestServer(t, nil)

	root := bodyString(t, rawGet(t, base, "/", nil))
	for _, want := range []string{"hello.txt", "sub", "report 2026.pdf", "junk.tmp", "LanShare"} {
		if !strings.Contains(root, want) {
			t.Errorf("根目录页缺少 %q", want)
		}
	}
	for _, no := range []string{".hidden.txt", "~$tmp.docx"} {
		if strings.Contains(root, no) {
			t.Errorf("根目录页不应出现隐藏项 %q", no)
		}
	}

	sub := bodyString(t, rawGet(t, base, "/?path="+url.QueryEscape("sub"), nil))
	for _, want := range []string{"data.bin", "中文文件.txt"} {
		if !strings.Contains(sub, want) {
			t.Errorf("子目录页缺少 %q", want)
		}
	}

	// 目录浏览不存在 → 404
	if c := statusOf(t, base, "/?path=ghost"); c != http.StatusNotFound {
		t.Errorf("不存在的目录应 404，实际 %d", c)
	}
}

func TestIgnoreRule(t *testing.T) {
	_, base := startTestServer(t, func(c *Config) { c.Ignore = []string{"*.tmp"} })
	root := bodyString(t, rawGet(t, base, "/", nil))
	if strings.Contains(root, "junk.tmp") {
		t.Error("Ignore 规则未生效：根目录页仍出现 junk.tmp")
	}
}

// ---------- 下载 / Range / HEAD ----------

func TestDownloadAndRange(t *testing.T) {
	_, base := startTestServer(t, nil)

	// 完整下载
	full := rawGet(t, base, "/dl?path=hello.txt", nil)
	if full.StatusCode != http.StatusOK {
		t.Fatalf("下载应 200，实际 %d", full.StatusCode)
	}
	if got := bodyString(t, full); got != "Hello, LanShare!\n" {
		t.Errorf("下载内容不符: %q", got)
	}

	// Range 断点（文件名含空格，经 QueryEscape 传参）
	rng := rawGet(t, base, "/dl?path="+url.QueryEscape("report 2026.pdf"), map[string]string{"Range": "bytes=0-3"})
	if rng.StatusCode != http.StatusPartialContent {
		t.Fatalf("Range 应 206，实际 %d", rng.StatusCode)
	}
	if got := bodyString(t, rng); got != "PDF-" {
		t.Errorf("Range 内容不符: %q", got)
	}
	if cr := rng.Header.Get("Content-Range"); !strings.HasPrefix(cr, "bytes 0-3/") {
		t.Errorf("Content-Range 不符: %q", cr)
	}

	// 中间段 Range
	rng2 := rawGet(t, base, "/dl?path=hello.txt", map[string]string{"Range": "bytes=7-12"})
	if rng2.StatusCode != http.StatusPartialContent {
		t.Fatalf("中间 Range 应 206，实际 %d", rng2.StatusCode)
	}
	if got := bodyString(t, rng2); got != "LanSha" { // "Hello, L" → 7..12 = "LanSha"
		t.Errorf("中间段内容不符: %q", got)
	}

	// HEAD：无 body
	headReq, _ := http.NewRequest(http.MethodHead, base+"/dl?path=hello.txt", nil)
	hr, err := http.DefaultClient.Do(headReq)
	if err != nil {
		t.Fatal(err)
	}
	hb := bodyString(t, hr)
	if hb != "" {
		t.Errorf("HEAD 应无 body，实际 %q", hb)
	}
	if hr.StatusCode != http.StatusOK {
		t.Errorf("HEAD 应 200，实际 %d", hr.StatusCode)
	}

	// 下载目录 → 302 转 zip
	dir := rawGet(t, base, "/dl?path=sub", nil)
	if dir.StatusCode != http.StatusFound {
		t.Errorf("下载目录应 302，实际 %d", dir.StatusCode)
	}
	if loc := dir.Header.Get("Location"); !strings.HasPrefix(loc, "/zip?paths=") {
		t.Errorf("目录下载 Location 不符: %q", loc)
	}
	dir.Body.Close()

	// 不存在 → 404
	if c := statusOf(t, base, "/dl?path=nope.bin"); c != http.StatusNotFound {
		t.Errorf("下载不存在文件应 404，实际 %d", c)
	}
}

// ---------- ZIP 打包 ----------

// getZip 请求打包并解析内存中的 zip，返回条目名 → 内容。
func getZip(t *testing.T, base, target string) (map[string]string, *http.Response) {
	t.Helper()
	resp := rawGet(t, base, target, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("ZIP %s 应 200，实际 %d", target, resp.StatusCode)
	}
	b, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	zr, err := zip.NewReader(bytes.NewReader(b), int64(len(b)))
	if err != nil {
		t.Fatalf("响应不是合法 zip: %v", err)
	}
	out := make(map[string]string, len(zr.File))
	for _, f := range zr.File {
		if f.FileInfo().IsDir() {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		c, _ := io.ReadAll(rc)
		rc.Close()
		out[f.Name] = string(c)
	}
	return out, resp
}

func TestZipVariants(t *testing.T) {
	_, base := startTestServer(t, nil)

	// 单个文件
	one, _ := getZip(t, base, "/zip?paths=hello.txt")
	if len(one) != 1 || one["hello.txt"] != "Hello, LanShare!\n" {
		t.Errorf("单文件 zip 不符: %v", one)
	}

	// 多选文件
	multi, _ := getZip(t, base, "/zip?paths="+url.QueryEscape("hello.txt,sub/data.bin"))
	if len(multi) != 2 {
		t.Fatalf("多文件 zip 条目数 = %d, 期望 2", len(multi))
	}
	if multi["sub/data.bin"] != bin255() {
		t.Error("data.bin 内容不符（0..255 字节序）")
	}

	// 整个共享根（paths 为空 → 打根，zip 内以根目录同名文件夹包裹）；
	// 隐藏项不得进包
	whole, _ := getZip(t, base, "/zip?paths=")
	hasSuffix := func(suf string) bool {
		for name := range whole {
			if name == suf || strings.HasSuffix(name, "/"+suf) {
				return true
			}
		}
		return false
	}
	for _, want := range []string{"hello.txt", "sub/data.bin", "sub/中文文件.txt"} {
		if !hasSuffix(want) {
			t.Errorf("全根 zip 缺少 %q（实际条目: %v）", want, keys(whole))
		}
	}
	for _, no := range []string{".hidden.txt", "~$tmp.docx"} {
		if hasSuffix(no) {
			t.Errorf("全根 zip 不应包含 %q", no)
		}
	}

	// 参数缺失 → 400
	if c := statusOf(t, base, "/zip"); c != http.StatusBadRequest {
		t.Errorf("/zip 缺参数应 400，实际 %d", c)
	}
}

// ---------- 路径攻击 ----------

func TestPathAttacksNeverSucceed(t *testing.T) {
	_, base := startTestServer(t, nil)
	// 每个样本：若服务端处理越界/非法路径并成功返回 200 即安全漏洞
	attacks := []string{
		"../x",
		"../../x",
		"..%2f..%2fx",
		"..\\..\\x",
		"a/../x",
		"C:/windows",
		"C:\\windows",
		`C:\windows\win.ini`,
		"hello.txt:ads", // NTFS 备用数据流
		"\\\\server\\share",
		"CON",
		"COM1",
		"sub/../../x",
		"trail.txt.",
		"trail.txt ",
	}
	for _, atk := range attacks {
		for _, prefix := range []string{"/dl?path=", "/?path="} {
			target := prefix + url.QueryEscape(atk)
			code := statusOf(t, base, target)
			if code == http.StatusOK {
				t.Errorf("路径攻击未拦截: %s → 200", target)
			}
			if code != http.StatusBadRequest && code != http.StatusForbidden && code != http.StatusNotFound {
				t.Errorf("路径攻击 %s 状态异常: %d（期望 400/403/404）", target, code)
			}
		}
	}
}

// ---------- 健康检查 ----------

func TestHealthz(t *testing.T) {
	_, base := startTestServer(t, nil)
	resp := rawGet(t, base, "/healthz", nil)
	if resp.StatusCode != http.StatusOK || bodyString(t, resp) != "ok" {
		t.Errorf("/healthz 异常")
	}
}

// ---------- 认证流程（开启密码） ----------

func TestAuthFlow(t *testing.T) {
	_, base := startTestServer(t, func(c *Config) { c.Password = "s3cret" })

	// 未登录访问根 → 302 到登录页并带 next
	root := rawGet(t, base, "/", nil)
	if root.StatusCode != http.StatusFound {
		t.Fatalf("未登录应 302，实际 %d", root.StatusCode)
	}
	if loc := root.Header.Get("Location"); !strings.HasPrefix(loc, "/login?next=") {
		t.Errorf("重定向 Location 不符: %q", loc)
	}
	root.Body.Close()

	// 登录页 GET
	login := bodyString(t, rawGet(t, base, "/login", nil))
	if !strings.Contains(login, "访问密码") {
		t.Error("登录页缺少提示文案")
	}

	// 错误密码：200 + 提示 + 无会话 cookie
	wrong := rawGet(t, base, "/login", nil)
	form := url.Values{"password": {"badpass"}}.Encode()
	req, _ := http.NewRequest(http.MethodPost, base+"/login", strings.NewReader(form))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	wresp, err := (&http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body := bodyString(t, wresp)
	if wresp.StatusCode != http.StatusOK || !strings.Contains(body, "密码错误") {
		t.Errorf("错误密码应提示重试（200），实际 %d", wresp.StatusCode)
	}
	if sc := wresp.Header.Get("Set-Cookie"); strings.Contains(sc, "lanshare_session=") {
		t.Error("密码错误不应种会话 cookie")
	}
	wrong.Body.Close()

	// 等待退避窗口结束（错误尝试后 300ms 起指数退避），再试正确密码
	time.Sleep(400 * time.Millisecond)

	// 正确密码（隐藏域带 next 回跳）
	form = url.Values{"password": {"s3cret"}, "next": {"/dl?path=hello.txt"}}.Encode()
	req, _ = http.NewRequest(http.MethodPost, base+"/login", strings.NewReader(form))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	okResp, err := (&http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if okResp.StatusCode != http.StatusFound {
		t.Fatalf("正确密码应 302，实际 %d", okResp.StatusCode)
	}
	if loc := okResp.Header.Get("Location"); loc != "/dl?path=hello.txt" {
		t.Errorf("回跳 Location 不符: %q", loc)
	}
	cookie := ""
	for _, c := range okResp.Cookies() {
		if c.Name == cookieName {
			cookie = c.Value
		}
	}
	okResp.Body.Close()
	if cookie == "" {
		t.Fatal("成功登录未种会话 cookie")
	}

	// 带 cookie 访问受保护资源 → 200
	authed := rawGet(t, base, "/dl?path=hello.txt", map[string]string{"Cookie": cookieName + "=" + cookie})
	if authed.StatusCode != http.StatusOK {
		t.Fatalf("带会话访问应 200，实际 %d", authed.StatusCode)
	}
	if got := bodyString(t, authed); got != "Hello, LanShare!\n" {
		t.Errorf("登录后下载内容不符: %q", got)
	}

	// 退出登录后再访问 → 重新要求登录
	logout := rawGet(t, base, "/logout", map[string]string{"Cookie": cookieName + "=" + cookie})
	logout.Body.Close()
	if c := statusOf(t, base, "/"); c != http.StatusFound {
		t.Errorf("登出后应 302 回登录页，实际 %d", c)
	}
}
