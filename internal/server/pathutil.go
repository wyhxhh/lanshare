package server

import (
	"errors"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
)

// 路径安全层：覆盖 Windows 文件系统的全部常见攻击向量。
//
//   - `..` / `../` / `..\` 目录穿越
//   - 盘符（C:）、UNC（\\server\share）、NTFS 备用数据流（file.txt:hidden）
//   - `\\?\` 与 `\\.\` 超长路径/设备前缀
//   - 大小写不敏感变体、URL 编码绕过（%2e%2e、%5c）
//   - 符号链接 / junction 目录逃逸出共享根（对目标做 EvalSymlinks 前缀校验）
//   - Windows 保留名（CON/PRN/AUX/NUL/COM1-9/LPT1-9）
//   - 段尾点/空格（Windows 会静默裁剪，可能造成校验与实际访问不一致）
//
// 规则（统一先替换 `\` 为 `/`，再做段级校验），拒绝返回 errPathForbidden，
// 不存在返回 os.ErrNotExist（由 handler 决定 404）。

var errPathForbidden = errors.New("路径被拒绝：非法字符或越界访问")

// windowsReserved 保留设备名（含扩展名也禁止，如 CON.txt）。
var windowsReserved = regexp.MustCompile(`(?i)^(con|prn|aux|nul|com[1-9]|lpt[1-9])(\..*)?$`)

// isHiddenName 判断是否隐藏项：以 "." 开头（.git 等）或 Office 临时文件。
func isHiddenName(name string) bool {
	if strings.HasPrefix(name, ".") {
		return true
	}
	if strings.HasPrefix(name, "~$") {
		return true
	}
	return false
}

// cleanSegments 把 rel（URL 解码后的相对路径，/ 或 \ 分隔）拆成安全段。
// 返回清洗后的 "/" 连接相对路径；非法返回 errPathForbidden。
func cleanSegments(rel string, allowHidden bool) (string, error) {
	if rel == "" {
		return "", nil
	}
	rel = strings.ReplaceAll(rel, "\\", "/")

	if strings.HasPrefix(rel, "/") {
		// 允许表示"根"的单独 "/"，其余绝对形式拒绝
		if strings.Trim(rel, "/") == "" {
			return "", nil
		}
		return "", errPathForbidden
	}
	if strings.ContainsAny(rel, ":*?\"<>|") {
		return "", errPathForbidden // 盘符/ADS/通配符/重定向字符一律拒绝
	}

	segs := strings.Split(rel, "/")
	out := make([]string, 0, len(segs))
	for _, seg := range segs {
		switch seg {
		case "", ".":
			continue
		case "..":
			return "", errPathForbidden
		}
		// Windows 规范化陷阱：段尾点或空格会被系统静默裁剪，
		// 若存在裁剪则拒绝（防止 "a./../" 与 "a/../" 判定不一致）。
		trimmed := strings.TrimRight(seg, ". ")
		if trimmed != seg || seg == "" {
			return "", errPathForbidden
		}
		if windowsReserved.MatchString(seg) {
			return "", errPathForbidden
		}
		if !allowHidden && isHiddenName(seg) {
			return "", errPathForbidden
		}
		out = append(out, seg)
	}
	return strings.Join(out, "/"), nil
}

// resolveSafe 将客户端提供的相对路径解析为共享根内的绝对路径。
//
// 两层校验：
//  1. cleanSegments 段级清洗（拒绝 ..、盘符、ADS、保留名、隐藏项等）；
//  2. 对最终目标 EvalSymlinks，解析结果必须仍在 rootReal 前缀内 ——
//     覆盖目录级符号链接 / junction 指向共享根之外的逃逸。
//
// 目标不存在时返回 *os.PathError（调用方可判 os.IsNotExist 转 404）。
func (s *Server) resolveSafe(rel string) (string, error) {
	cleaned, err := cleanSegments(rel, s.cfg.ShowHidden)
	if err != nil {
		return "", err
	}
	abs := s.rootAbs
	if cleaned != "" {
		abs = filepath.Join(abs, filepath.FromSlash(cleaned))
	}

	// 存在性 + 符号链接解析（同时覆盖 root 自身与父目录链上的 junction）
	real, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", err // 不存在/无权限，交由 handler 转 404/403
	}
	if !withinPrefix(s.rootReal, real) {
		return "", errPathForbidden
	}
	return abs, nil
}

// withinPrefix 大小写不敏感地判断 child 是否位于 root 目录内（含边界）。
func withinPrefix(root, child string) bool {
	root = strings.TrimRight(filepath.Clean(root), `/\`)
	child = filepath.Clean(child)
	if !strings.EqualFold(root, child) {
		if !strings.HasPrefix(strings.ToLower(child), strings.ToLower(root)+string(filepath.Separator)) {
			return false
		}
	}
	return true
}

// ignoreMatch 判断 rel（已清洗的相对路径）是否命中任一忽略规则。
// 规则按 path.Match 语法匹配「任一路径段名」或「完整相对路径」。
func (s *Server) ignoreMatch(rel string) bool {
	if len(s.cfg.Ignore) == 0 {
		return false
	}
	rel = strings.ReplaceAll(rel, "\\", "/")
	segs := strings.Split(rel, "/")
	for _, pat := range s.cfg.Ignore {
		for _, seg := range segs {
			if seg == "" {
				continue
			}
			if ok, _ := path.Match(pat, seg); ok {
				return true
			}
		}
		if ok, _ := path.Match(pat, rel); ok {
			return true
		}
	}
	return false
}

// listVisibleEntries 列出目录下可展示条目：过滤隐藏项与忽略规则。
// 目录优先，各自按自然排序（file2 < file10）。
func (s *Server) listVisibleEntries(absDir string) ([]dirEntry, error) {
	f, err := os.Open(absDir)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	names, err := f.Readdirnames(-1)
	if err != nil {
		return nil, err
	}

	showHidden := s.cfg.ShowHidden
	entries := make([]dirEntry, 0, len(names))
	for _, n := range names {
		if !showHidden && isHiddenName(n) {
			continue
		}
		if s.ignoreMatch(n) {
			continue
		}
		full := filepath.Join(absDir, n)
		info, err := os.Lstat(full)
		if err != nil {
			continue // 竞态：读取时被删
		}
		// 符号链接/junction：跟随解析真实类型以决定展示为目录还是文件；
		// 悬空链接跳过（访问必失败）。越界链接在此仅决定展示，
		// 实际访问由 resolveSafe 的 EvalSymlinks 前缀校验拦截。
		target := info
		if info.Mode()&os.ModeSymlink != 0 {
			ti, err := os.Stat(full)
			if err != nil {
				continue
			}
			target = ti
		}
		entries = append(entries, dirEntry{
			Name:  n,
			IsDir: target.IsDir(),
			Size:  target.Size(),
			Mod:   target.ModTime(),
		})
	}
	sortDirEntries(entries)
	return entries, nil
}
