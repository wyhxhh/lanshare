//go:build !windows

package gui

// 非 Windows 平台（开发兜底）：不做单实例限制。
func acquireSingleInstance() (func(), error) {
	return func() {}, nil
}

func showAlreadyRunning() {}
