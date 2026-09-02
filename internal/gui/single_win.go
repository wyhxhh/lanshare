//go:build windows

package gui

import (
	"fmt"
	"syscall"
	"unsafe"
)

// 单实例互斥：使用命名 Mutex 防止重复启动（共享目录/端口/配置冲突）。
// 实现走 LazyDLL，不引入额外依赖；会话级命名空间（Local\）无需管理员权限。
//
// 说明：Mutex 由本进程持有至退出；进程结束后内核自动释放，
// 因此"上次异常退出"不会造成假占用。

var (
	kernel32         = syscall.NewLazyDLL("kernel32.dll")
	user32           = syscall.NewLazyDLL("user32.dll")
	procCreateMutexW = kernel32.NewProc("CreateMutexW")
	procCloseHandle  = kernel32.NewProc("CloseHandle")
	procMessageBoxW  = user32.NewProc("MessageBoxW")
)

const (
	mbIconInformation = 0x40
)

// acquireSingleInstance 尝试获取单实例互斥。
// 返回 release 需在进程退出前调用；errAlreadyRunning 表示已有实例在运行。
func acquireSingleInstance() (release func(), err error) {
	name, _ := syscall.UTF16PtrFromString(`Local\LanShare.Desktop`)
	h, _, callErr := procCreateMutexW.Call(0, 0, uintptr(unsafe.Pointer(name)))
	if h == 0 {
		return nil, fmt.Errorf("创建单实例互斥失败: %v", callErr)
	}
	// 命名互斥已存在时 CreateMutexW 仍返回有效句柄，仅 LastError = ERROR_ALREADY_EXISTS。
	// 此时互斥属于先前启动的实例 → 释放本句柄并报"已在运行"。
	if errno, ok := callErr.(syscall.Errno); ok && errno == syscall.ERROR_ALREADY_EXISTS {
		procCloseHandle.Call(h)
		return nil, errAlreadyRunning
	}
	return func() { procCloseHandle.Call(h) }, nil
}

// showAlreadyRunning 弹窗提示已有实例（仅提示，随即退出）。
func showAlreadyRunning() {
	text, _ := syscall.UTF16PtrFromString("LanShare 已在运行。\n请打开已运行窗口的托盘图标，或先退出再启动。")
	caption, _ := syscall.UTF16PtrFromString("LanShare")
	procMessageBoxW.Call(0, uintptr(unsafe.Pointer(text)), uintptr(unsafe.Pointer(caption)), mbIconInformation)
}
