package server

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestConcurrentMixedLoad 多人高并发压测（真实 TCP + 并发 HTTP 客户端）：
//
//	目录列表 / Range 分片下载（校验逐字节一致）/ 整目录流式 ZIP / HEAD / 小文件下载 / 健康检查。
//
// 由 LSH_LOAD=1 显式开启（避免拖慢常规回归），用法：
//
//	LSH_LOAD=1 go test ./internal/server/ -run TestConcurrentMixedLoad -v -count=1
func TestConcurrentMixedLoad(t *testing.T) {
	if os.Getenv("LSH_LOAD") == "" {
		t.Skip("并发压测需显式开启：LSH_LOAD=1")
	}

	root := t.TempDir()
	const bigSize = 8 << 20 // 8MB 大文件
	big := make([]byte, bigSize)
	for i := range big {
		big[i] = byte(i%251 + (i>>12)%7)
	}
	mustWrite := func(rel string, b []byte) {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	mustWrite("big.bin", big)
	mustWrite("small.txt", []byte("small-file-content\n"))
	// 目录内容用 LCG 伪随机填充（不可压缩），确保 ZIP 任务实测的是真实吞吐，
	// 而不是 deflate 把强规律数据压成几百字节后的假象
	for i := 0; i < 8; i++ {
		mustWrite(fmt.Sprintf("bundle/f%d.bin", i), randBytes(256<<10, uint32(1000+i)))
	}
	mustWrite("bundle/nested/a.bin", randBytes(64<<10, 7))
	mustWrite("bundle/nested/b.bin", randBytes(64<<10, 9))

	srv, err := New(Config{Root: root, Port: -1}, "loadtest")
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
	base := "http://127.0.0.1:" + strconv.Itoa(srv.Port())

	const (
		clients = 32
		iters   = 8 // 每客户端串行 8 个任务 → 共 256 个请求，峰值并发 = 32 台客户端
	)
	tr := &http.Transport{MaxIdleConnsPerHost: 128, MaxIdleConns: 256}
	client := &http.Client{Transport: tr}
	defer tr.CloseIdleConnections()

	errCh := make(chan string, clients*iters)
	var (
		mu      sync.Mutex
		ops     int
		bytes   int64 // 响应体字节合计（吞吐口径）
		startAt = time.Now()
		wg      sync.WaitGroup
	)

	runJob := func(job int) {
		var b int64
		kind := job % 10
		switch {
		case kind == 0: // 整目录 ZIP（流式 ~2MB）
			resp, err := client.Get(base + "/zip?paths=bundle")
			if err == nil {
				n, _ := io.Copy(io.Discard, resp.Body)
				resp.Body.Close()
				if resp.StatusCode != 200 || n < 1<<20 {
					errCh <- fmt.Sprintf("zip status=%d bytes=%d", resp.StatusCode, n)
					return
				}
				b = n
			} else {
				errCh <- "zip: " + err.Error()
				return
			}
		case kind == 1: // 目录列表
			resp, err := client.Get(base + "/?path=bundle")
			if err == nil {
				body, _ := io.ReadAll(resp.Body)
				resp.Body.Close()
				if resp.StatusCode != 200 || !strings.Contains(string(body), "f0.bin") {
					errCh <- "list bundle failed"
					return
				}
				b = int64(len(body))
			} else {
				errCh <- "list: " + err.Error()
				return
			}
		case kind == 2, kind == 3: // Range 分片下载大文件（校验逐字节一致）
			off := int64(job%997) * 4096
			req, _ := http.NewRequest("GET", base+"/dl?path=big.bin", nil)
			req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", off, off+131071))
			resp, err := client.Do(req)
			if err != nil {
				errCh <- "range req: " + err.Error()
				return
			}
			got, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			want := big[off : off+131072]
			if resp.StatusCode != 206 || !bytesEqual(got, want) {
				errCh <- fmt.Sprintf("range off=%d status=%d len=%d", off, resp.StatusCode, len(got))
				return
			}
			b = int64(len(got))
		case kind == 4: // HEAD 大文件
			req, _ := http.NewRequest("HEAD", base+"/dl?path=big.bin", nil)
			resp, err := client.Do(req)
			if err != nil {
				errCh <- "head: " + err.Error()
				return
			}
			resp.Body.Close()
			if resp.StatusCode != 200 || resp.ContentLength != bigSize {
				errCh <- fmt.Sprintf("head status=%d len=%d", resp.StatusCode, resp.ContentLength)
			}
		case job%7 == 0: // 健康检查
			resp, err := client.Get(base + "/healthz")
			if err != nil {
				errCh <- "healthz: " + err.Error()
				return
			}
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			if string(body) != "ok" {
				errCh <- "healthz failed"
			}
		default: // 小文件整包下载（校验内容）
			resp, err := client.Get(base + "/dl?path=small.txt")
			if err != nil {
				errCh <- "small: " + err.Error()
				return
			}
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			if string(body) != "small-file-content\n" {
				errCh <- "small download mismatch"
				return
			}
			b = int64(len(body))
		}
		mu.Lock()
		ops++
		bytes += b
		mu.Unlock()
	}

	// 32 个客户端连接各自串行执行 8 个任务：峰值并发 = 32（真实"多人在用"场景），
	// 而不是 256 个请求在同一瞬间打爆 accept 队列
	for c := 0; c < clients; c++ {
		wg.Add(1)
		go func(c int) {
			defer wg.Done()
			for k := 0; k < iters; k++ {
				runJob(c*iters + k)
			}
		}(c)
	}
	wg.Wait()
	close(errCh)
	elapsed := time.Since(startAt)

	fails := 0
	for e := range errCh {
		fails++
		if fails <= 10 {
			t.Errorf("并发任务失败: %s", e)
		}
	}
	if fails > 0 {
		t.Fatalf("共 %d 个任务失败", fails)
	}

	mb := float64(bytes) / (1 << 20)
	t.Logf("并发压测通过：%d 客户端 × %d 任务 = %d 请求，0 失败", clients, iters, ops)
	t.Logf("传输合计 %.1f MB，耗时 %s，实测吞吐 %.1f MB/s",
		mb, elapsed.Round(time.Millisecond), mb/elapsed.Seconds())
	t.Logf("服务端计数：累计发送 %s · 压测结束时活跃请求 %d",
		humanSize(srv.BytesSent()), srv.ActiveRequests())
}

func randBytes(n int, seed uint32) []byte {
	b := make([]byte, n)
	x := seed*2654435761 + 0x9e3779b9
	for i := range b {
		x = x*1664525 + 1013904223
		b[i] = byte(x >> 16)
	}
	return b
}

func bytesEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
