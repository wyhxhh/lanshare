package server

import (
	"strings"
	"testing"
)

// ---- cleanSegments：路径攻击矩阵（Windows 全覆盖） ----

func TestCleanSegments(t *testing.T) {
	cases := []struct {
		rel         string
		allowHidden bool
		want        string
		wantErr     bool
	}{
		// 正常路径
		{"", false, "", false},
		{"a", false, "a", false},
		{"a/b", false, "a/b", false},
		{"a\\b", false, "a/b", false},
		{"a//b/./c", false, "a/b/c", false},
		{"/", false, "", false}, // 单独的 "/" 视为根
		{"中文 目录/报告 (2026).pdf", false, "中文 目录/报告 (2026).pdf", false},
		{"name with spaces.txt", false, "name with spaces.txt", false},
		{"COM10", false, "COM10", false}, // Windows 只保留 COM1-9
		{".git", true, ".git", false},    // 开启显示隐藏后允许
		{"~$doc.docx", true, "~$doc.docx", false},

		// 目录穿越
		{"..", false, "", true},
		{"../x", false, "", true},
		{"a/../b", false, "", true},
		{"a/..", false, "", true},
		{"..\\x", false, "", true},

		// 盘符 / UNC / ADS / 设备前缀
		{"C:", false, "", true},
		{"C:/x", false, "", true},
		{"c:\\x", false, "", true},
		{"a:b", false, "", true},
		{"file.txt:ads", false, "", true},
		{"\\\\srv\\share", false, "", true}, // 经替换成 //srv//share → 绝对形式拒绝
		{"/abs/path", false, "", true},

		// 非法通配/重定向字符
		{"a?b", false, "", true},
		{"a*b", false, "", true},
		{`a"b`, false, "", true},
		{"a<b", false, "", true},
		{"a>b", false, "", true},
		{"a|b", false, "", true},

		// Windows 裁剪陷阱：段尾点/空格
		{"trail.txt ", false, "", true},
		{"trail.txt.", false, "", true},
		{"a/trail ", false, "", true},

		// 保留设备名（含扩展名变体）
		{"CON", false, "", true},
		{"con.txt", false, "", true},
		{"NUL", false, "", true},
		{"nul.log", false, "", true},
		{"PRN", false, "", true},
		{"AUX", false, "", true},
		{"LPT2", false, "", true},
		{"COM9.txt", false, "", true},

		// 隐藏项默认过滤
		{".git", false, "", true},
		{".hidden/x", false, "", true},
		{"~$doc.docx", false, "", true},
	}

	for _, c := range cases {
		got, err := cleanSegments(c.rel, c.allowHidden)
		if c.wantErr {
			if err == nil {
				t.Errorf("cleanSegments(%q) 期望拒绝，实际通过 → %q", c.rel, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("cleanSegments(%q) 不应报错: %v", c.rel, err)
			continue
		}
		if got != c.want {
			t.Errorf("cleanSegments(%q) = %q, 期望 %q", c.rel, got, c.want)
		}
	}
}

// ---- naturalLess：自然排序 ----

func TestNaturalLess(t *testing.T) {
	ok := [][2]string{
		{"file2", "file10"},
		{"a1b2", "a1b10"},
		{"1", "2"},
		{"v1.0", "v1.10"},
		{"a", "b"},
	}
	for _, p := range ok {
		if !naturalLess(p[0], p[1]) {
			t.Errorf("naturalLess(%q, %q) 应为 true", p[0], p[1])
		}
		if naturalLess(p[1], p[0]) {
			t.Errorf("naturalLess(%q, %q) 应为 false（对称性）", p[1], p[0])
		}
	}
}

// ---- withinPrefix：大小写不敏感前缀边界 ----

func TestWithinPrefix(t *testing.T) {
	ok := [][2]string{
		{`C:\share`, `C:\share`},
		{`C:\share`, `c:\SHARE\a\b`},
		{`C:\share`, `C:\share\a`},
		{`C:\share`, `C:\share\`},
		{`C:\share\x`, `C:\share\x\y\..\z`}, // Clean 归一化后仍在 x 内
	}
	for _, p := range ok {
		if !withinPrefix(p[0], p[1]) {
			t.Errorf("withinPrefix(%q, %q) 应为 true", p[0], p[1])
		}
	}
	bad := [][2]string{
		{`C:\share`, `C:\share2`},
		{`C:\share`, `C:\share2\a`},
		{`C:\share`, `D:\share`},
		{`C:\share\a`, `C:\share`},        // 子不能当父
		{`C:\share\x`, `C:\share\x\..\y`}, // 经 .. 逃出 x 后即使仍落在 share 内也不属于 x
		{`C:\share\x`, `C:\share`},        // Clean 后逃到 x 之上 → 拒绝
	}
	for _, p := range bad {
		if withinPrefix(p[0], p[1]) {
			t.Errorf("withinPrefix(%q, %q) 应为 false", p[0], p[1])
		}
	}
}

// ---- ignoreMatch：忽略规则 ----

func TestIgnoreMatch(t *testing.T) {
	s := &Server{cfg: Config{Ignore: []string{"*.tmp", "cache", "sub/*", "*.log"}}}
	yes := []string{"a/x.tmp", "a/b/c.tmp", "cache", "cache/x.txt", "sub/a", "run.log"}
	no := []string{"a/x.txt", "sub", "sub2/a", "a/cachex", "readme.md"}

	for _, rel := range yes {
		if !s.ignoreMatch(rel) {
			t.Errorf("ignoreMatch(%q) 应为 true", rel)
		}
	}
	for _, rel := range no {
		if s.ignoreMatch(rel) {
			t.Errorf("ignoreMatch(%q) 应为 false", rel)
		}
	}
}

// ---- isHiddenName ----

func TestIsHiddenName(t *testing.T) {
	for _, n := range []string{".git", ".DS_Store", "~$w.docx", ".x"} {
		if !isHiddenName(n) {
			t.Errorf("isHiddenName(%q) 应为 true", n)
		}
	}
	for _, n := range []string{"a.txt", "~x", "$x", "报告.pdf"} {
		if isHiddenName(n) {
			t.Errorf("isHiddenName(%q) 应为 false", n)
		}
	}
}

// ---- contentDisposition：中文文件名 ----

func TestContentDisposition(t *testing.T) {
	got := contentDisposition("报告 (1).pdf")
	if !strings.Contains(got, "filename*=UTF-8''") {
		t.Errorf("缺少 RFC 5987 filename*: %s", got)
	}
	if !strings.Contains(got, "%E6%8A%A5%E5%91%8A") { // "报告" 的 PathEscape
		t.Errorf("filename* 未正确转义中文: %s", got)
	}
}
