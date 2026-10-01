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

// normalizeProxyServer 处理 "host:port" 与 "http=h:p;https=h:p" 两种写法。
func normalizeProxyServer(server string) string {
	server = strings.TrimSpace(server)
	if server == "" {
		return ""
	}
	if strings.Contains(server, "=") {
		pick := ""
		for _, part := range strings.Split(server, ";") {
			kv := strings.SplitN(part, "=", 2)
			if len(kv) != 2 {
				continue
			}
			if strings.EqualFold(strings.TrimSpace(kv[0]), "https") {
				pick = strings.TrimSpace(kv[1])
				break
			}
			if pick == "" {
				pick = strings.TrimSpace(kv[1])
			}
		}
		server = pick
	}
	if server == "" {
		return ""
	}
	if !strings.Contains(server, "://") {
		server = "http://" + server
	}
	return server
}
