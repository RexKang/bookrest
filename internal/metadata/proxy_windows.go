//go:build windows

package metadata

import (
	"strings"

	"golang.org/x/sys/windows/registry"
)

// systemProxy 读 Windows 的 WinINET 代理设置（用户级）。
// 本机的代理软件通常只写这里、不设环境变量，所以必须读注册表。
func systemProxy() string {
	k, err := registry.OpenKey(registry.CURRENT_USER,
		`Software\Microsoft\Windows\CurrentVersion\Internet Settings`, registry.QUERY_VALUE)
	if err != nil {
		return ""
	}
	defer k.Close()
	enable, _, err := k.GetIntegerValue("ProxyEnable")
	if err != nil || enable == 0 {
		return ""
	}
	server, _, err := k.GetStringValue("ProxyServer")
	if err != nil {
		return ""
	}
	return normalizeProxyServer(strings.TrimSpace(server))
}
