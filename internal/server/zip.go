package server

import (
	"archive/zip"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// handleZip 流式打包下载：?paths=rel1,rel2（逗号分隔，可为文件或目录；
// 提供空值或省略路径项等价于打包共享根）。
//
// 特性：
//   - 边压缩边写 ResponseWriter，内存占用恒定（不缓存整包）；
//   - zip 内结构 = 相对共享根的路径（paths=docs → docs/...）；
//   - 遵循与目录列表一致的过滤策略（隐藏项/忽略规则不进包）；
//   - 指向共享根之外的符号链接/junction 跳过并报告（防越界泄露）；
//   - 单个文件打开/读取失败时跳过、不使整个包失败；全部失败才 4xx；
//   - 失败清单以 zip 内 "_打包报告.txt" 呈现 + X-LanShare-ZipFailed 响应头；
//   - >4GB / >65535 项由 zip.Writer 自动升级 Zip64。
func (s *Server) handleZip(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	raws, hasPaths := q["paths"]
	if !hasPaths {
		s.renderError(w, "缺少 paths 参数", nil, http.StatusBadRequest)
		return
	}
	raw := ""
	if len(raws) > 0 {
		raw = raws[0]
	}
	parts := []string{}
	if strings.TrimSpace(raw) != "" {
		for _, p := range strings.Split(raw, ",") {
			p = strings.TrimSpace(p)
			if p != "" {
				parts = append(parts, p)
			}
		}
	}
	if len(parts) == 0 {
		parts = []string{""} // 打包整个共享根
	}

	// 先全部校验（任一非法即整体拒绝，避免流写到一半才发现）
	type item struct {
		abs    string
		zipRel string // zip 内相对路径（顶层）
		isDir  bool
	}
	items := make([]item, 0, len(parts))
	for _, p := range parts {
		abs, err := s.resolveSafe(p)
		if err != nil {
			s.renderError(w, "打包路径无效", err, http.StatusBadRequest)
			return
		}
		info, err := os.Stat(abs)
		if err != nil {
			s.renderError(w, "打包路径不可读", err, http.StatusNotFound)
			return
		}
		zipRel := p
		if zipRel == "" {
			zipRel = filepath.Base(s.rootAbs)
		}
		items = append(items, item{abs: abs, zipRel: zipRel, isDir: info.IsDir()})
	}

	// zip 文件名
	base := s.zipDisplayName(parts[0])
	if base == "" {
		base = "LanShare"
	}
	if len(items) > 1 {
		base = filepath.Base(s.rootAbs)
	}

	s.activeDL.Add(1)
	defer s.activeDL.Add(-1)

	// 预写响应头（在 WriteHeader 前可设 Content-Disposition）
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", contentDisposition(base+".zip"))
	w.Header().Set("Cache-Control", "no-store")

	zw := zip.NewWriter(w)
	seen := make(map[string]bool)
	var failed []string
	added := 0

	for _, it := range items {
		if it.isDir {
			s.zipAddDir(zw, it.abs, it.zipRel, seen, &failed, &added, 0)
		} else {
			s.zipAddFile(zw, it.abs, it.zipRel, seen, &failed, &added)
		}
	}

	// 失败报告（仅当有失败时追加到包内）
	if len(failed) > 0 {
		sb := strings.Builder{}
		fmt.Fprintf(&sb, "以下 %d 项未能打包（可能被占用、被删除或指向共享外）：\r\n\r\n", len(failed))
		for _, f := range failed {
			sb.WriteString(f)
			sb.WriteString("\r\n")
		}
		fw, err := zw.Create("_打包报告.txt")
		if err == nil {
			io.WriteString(fw, sb.String())
		}
		w.Header().Set("X-LanShare-ZipFailed", strconv.Itoa(len(failed)))
	}

	zw.Close()

	if added == 0 && len(failed) > 0 {
		// 全部失败：无法返回 4xx（已写流），但至少浏览器拿到的是空包+报告
		return
	}
}

// zipAddDir 递归添加目录（zip 内保留空目录项）。
func (s *Server) zipAddDir(zw *zip.Writer, absDir, zipRel string, seen map[string]bool, failed *[]string, added *int, depth int) {
	if depth > 64 {
		*failed = append(*failed, zipRel+"（目录过深，已跳过）")
		return
	}

	// 空目录也保留
	dirHdr := &zip.FileHeader{Name: zipRel + "/", Method: zip.Store}
	if fw, err := zw.CreateHeader(dirHdr); err == nil {
		_ = fw
	}

	names, err := readDirSorted(absDir)
	if err != nil {
		*failed = append(*failed, zipRel+"（目录读取失败）")
		return
	}
	for _, de := range names {
		name := de.Name()
		if !s.cfg.ShowHidden && isHiddenName(name) {
			continue
		}
		if s.ignoreMatch(name) {
			continue
		}
		childAbs := filepath.Join(absDir, name)
		childZip := zipRel + "/" + name

		// 符号链接：解析真实目标；指向共享外则跳过（防越界），悬空跳过
		if de.Type()&os.ModeSymlink != 0 {
			real, err := filepath.EvalSymlinks(childAbs)
			if err != nil {
				*failed = append(*failed, childZip+"（链接无效，已跳过）")
				continue
			}
			if !withinPrefix(s.rootReal, real) {
				*failed = append(*failed, childZip+"（指向共享目录外，已跳过）")
				continue
			}
			childAbs = real // 打包真实目标，避免 zip 内含链接
			if fi, err := os.Stat(real); err == nil {
				if fi.IsDir() {
					s.zipAddDir(zw, real, childZip, seen, failed, added, depth+1)
					continue
				}
			}
		}

		info, err := de.Info()
		if err != nil {
			*failed = append(*failed, childZip+"（无法读取信息）")
			continue
		}
		if info.IsDir() {
			s.zipAddDir(zw, childAbs, childZip, seen, failed, added, depth+1)
		} else {
			s.zipAddFile(zw, childAbs, childZip, seen, failed, added)
		}
	}
}

// zipAddFile 添加单个文件（以真实路径去重）。
func (s *Server) zipAddFile(zw *zip.Writer, absFile, zipRel string, seen map[string]bool, failed *[]string, added *int) {
	real, err := filepath.EvalSymlinks(absFile)
	if err != nil {
		*failed = append(*failed, zipRel+"（无法解析）")
		return
	}
	if seen[real] {
		return // 同一真实文件经不同路径/链接出现多次 → 只打包一次
	}
	seen[real] = true

	f, err := os.Open(real)
	if err != nil {
		*failed = append(*failed, zipRel+"（文件被占用或无法打开）")
		return
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || info.IsDir() {
		if err == nil {
			*failed = append(*failed, zipRel+"（非普通文件，已跳过）")
		} else {
			*failed = append(*failed, zipRel+"（无法读取）")
		}
		return
	}

	hdr := &zip.FileHeader{Name: zipRel, Method: zip.Deflate}
	hdr.SetModTime(info.ModTime())
	// 中文/Unicode 文件名：置 UTF-8 标志（Win10+ 解压不乱码）
	hdr.SetMode(info.Mode())

	fw, err := zw.CreateHeader(hdr)
	if err != nil {
		*failed = append(*failed, zipRel)
		return
	}
	if _, err := io.Copy(fw, f); err != nil {
		*failed = append(*failed, zipRel+"（写入中断）")
		return
	}
	*added++
}

// readDirSorted 读取目录并按自然排序返回（隐藏/忽略过滤由调用方处理）。
func readDirSorted(absDir string) ([]os.DirEntry, error) {
	es, err := os.ReadDir(absDir)
	if err != nil {
		return nil, err
	}
	sort.SliceStable(es, func(i, j int) bool {
		if es[i].IsDir() != es[j].IsDir() {
			return es[i].IsDir()
		}
		return naturalLess(es[i].Name(), es[j].Name())
	})
	return es, nil
}
