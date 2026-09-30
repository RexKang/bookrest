package app

import (
	"os/exec"
	"path/filepath"
	"runtime"
)

// openPath 用系统默认程序打开文件（FR-8.1）；exe 非空时用指定程序（FR-8.3）。
func openPath(abs, exe string) error {
	if exe != "" {
		return exec.Command(exe, abs).Start()
	}
	switch runtime.GOOS {
	case "windows":
		// 注意：不要经 shell 拼接路径，start 的第一个空参数是窗口标题占位
		return exec.Command("cmd", "/c", "start", "", filepath.FromSlash(abs)).Start()
	case "darwin":
		return exec.Command("open", abs).Start()
	default:
		return exec.Command("xdg-open", abs).Start()
	}
}

// revealPath 在文件管理器中定位文件（FR-8.2）。
func revealPath(abs string) error {
	abs = filepath.FromSlash(abs)
	switch runtime.GOOS {
	case "windows":
		return exec.Command("explorer", "/select,"+abs).Start()
	case "darwin":
		return exec.Command("open", "-R", abs).Start()
	default:
		return exec.Command("xdg-open", filepath.Dir(abs)).Start()
	}
}
