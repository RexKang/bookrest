//go:build !windows

package metadata

// systemProxy 在非 Windows 平台没有「系统代理」概念（用环境变量即可）。
func systemProxy() string { return "" }
