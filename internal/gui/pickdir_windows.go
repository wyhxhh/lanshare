//go:build windows

package gui

// Windows 系统原生文件夹选择器（SHBrowseForFolderW）。
// 相比手工 COM IFileOpenDialog vtable 调用，SHBrowseForFolderW 是 shell32 的单一
// 导出函数、传 BROWSEINFO 结构体指针即可：无 vtable、无 COM 线程模式依赖，
// 稳定得多；对话框仍是系统原生（新版树形样式，含地址栏与「新建文件夹」）。
// 初始目录通过浏览回调（BFFM_INITIALIZED → BFFM_SETSELECTIONW）定位。

import (
	"syscall"
	"unsafe"
)

var (
	modShell32            = syscall.NewLazyDLL("shell32.dll")
	modOle32              = syscall.NewLazyDLL("ole32.dll")
	modUser32             = syscall.NewLazyDLL("user32.dll")
	procSHBrowseForFolder = modShell32.NewProc("SHBrowseForFolderW")
	procSHGetPathFromIDL  = modShell32.NewProc("SHGetPathFromIDListW")
	procCoTaskMemFree     = modOle32.NewProc("CoTaskMemFree")
	procSendMessageW      = modUser32.NewProc("SendMessageW")
)

// BROWSEINFOW 结构（unicode 版）。
type browseInfoW struct {
	hwndOwner      uintptr
	pidlRoot       uintptr
	pszDisplayName *uint16 // 用户所选目录的显示名缓冲（也可作为缺省名）
	lpszTitle      *uint16 // 对话框标题
	ulFlags        uint32
	lpfn           uintptr // 浏览回调（可选）
	lParam         uintptr // 传给回调的附加数据
	iImage         int32
}

// 浏览对话框消息与标志
const (
	bifReturnOnlyFSDirs = 0x0001 // 仅文件系统目录
	bifNewDialogStyle   = 0x0040 // 新版对话框样式（地址栏/新建文件夹）

	bffmInitialized   = 1            // 回调消息：对话框已初始化
	bffmSetSelectionW = 0x0400 + 103 // BFFM_SETSELECTION = WM_USER + 103
	maxPickPath       = 32768        // 选择缓冲（容纳超长路径）
)

// browseCallbackProc 对话框回调：初始化完成后把上次选择的目录定位进地址栏。
// lpData 携带 lParam 中的初始目录 *uint16（UTF-16）。
func browseCallbackProc(hwnd uintptr, uMsg uint32, lParam, lpData uintptr) uintptr {
	if uMsg == bffmInitialized && lpData != 0 {
		procSendMessageW.Call(hwnd, bffmSetSelectionW, 1, lpData)
	}
	return 0
}

var browseCallback = syscall.NewCallback(browseCallbackProc)

// nativePickFolder 弹出系统原生「选择文件夹」对话框（模态阻塞）。
// initial 为初始定位目录（可为空）；ok=false 表示取消或失败。
func nativePickFolder(initial string) (path string, ok bool) {
	title, err := syscall.UTF16PtrFromString("选择要共享的文件夹")
	if err != nil {
		return "", false
	}
	display := make([]uint16, maxPickPath)
	bi := browseInfoW{
		pszDisplayName: &display[0],
		lpszTitle:      title,
		ulFlags:        bifReturnOnlyFSDirs | bifNewDialogStyle,
	}
	if initial != "" {
		if p, err := syscall.UTF16PtrFromString(initial); err == nil {
			bi.lpfn = browseCallback
			bi.lParam = uintptr(unsafe.Pointer(p))
		}
	}

	// 模态运行：阻塞直到用户选择/取消（内部自带消息循环）
	pidl, _, _ := procSHBrowseForFolder.Call(uintptr(unsafe.Pointer(&bi)))
	if pidl == 0 {
		return "", false // 用户取消（pidl 为 NULL）
	}
	defer procCoTaskMemFree.Call(pidl)

	buf := make([]uint16, maxPickPath)
	r, _, _ := procSHGetPathFromIDL.Call(pidl, uintptr(unsafe.Pointer(&buf[0])))
	if r == 0 {
		return "", false
	}
	p := syscall.UTF16ToString(buf) // 遇 NUL 截断
	if p == "" {
		return "", false
	}
	return p, true
}

// pickDir Windows 实现：调用系统原生「选择文件夹」对话框。
// 对话框模态运行在独立 goroutine（内部自带消息循环），不阻塞 Fyne 主循环；
// 选择结果通过 postUI 安全回到主协程写入输入框。
func (g *gui) pickDir() {
	g.browseBtn.Disable() // 防重入：对话框弹出期间禁用，避免连点叠出多个
	initial := g.dirEntry.Text
	go func() {
		dir, ok := nativePickFolder(initial)
		g.postUI(func() {
			if ok && dir != "" {
				g.dirEntry.SetText(dir)
			}
			g.browseBtn.Enable()
		})
	}()
}
