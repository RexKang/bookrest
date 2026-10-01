// Package server 是 B/S 传输：把应用层暴露成 HTTP + 静态资源，浏览器访问同一套前端。
//
// 安全模型（本地软件优先）：
//  1. 默认只绑本机回环地址；绑定非回环地址必须显式授权（Options.AllowRemote），
//     否则直接拒绝启动——不会「悄悄」把书架暴露到局域网。
//  2. 一律要求访问令牌（?token= 首次进入 → HttpOnly + SameSite=Strict Cookie），
//     未带令牌的请求返回 401。令牌每次启动随机生成，不落盘。
//  3. 校验 Host 头（只认本机名与本机地址），阻断 DNS rebinding 类攻击。
//  4. 不发送任何 CORS 头；SameSite=Strict 阻断跨站请求伪造。
package server

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/RexKang/bookrest/internal/app"
	"github.com/RexKang/bookrest/internal/config"
	"github.com/RexKang/bookrest/internal/metadata"
	"github.com/RexKang/bookrest/internal/store"
)

const cookieName = "bookrest_token"

type Options struct {
	Addr        string // 监听地址，默认 127.0.0.1:8788
	Dist        fs.FS  // 内嵌静态资源（生产模式）
	DistDir     string // 磁盘静态资源目录（开发模式，改动即时可见）
	Dev         bool
	Token       string // 访问令牌；留空则自动生成
	AllowRemote bool   // 显式授权：允许绑定非本机地址（局域网访问）
	Quiet       bool   // 不打印启动信息（测试用）
}

// Handler 校验并装配 HTTP 处理器；返回规范化后的 Options（含生成的令牌）。
func Handler(a *app.App, opts Options) (http.Handler, Options, error) {
	host, _, err := net.SplitHostPort(opts.Addr)
	if err != nil {
		return nil, opts, fmt.Errorf("监听地址不合法（应形如 127.0.0.1:8788）：%w", err)
	}
	loopback := isLoopbackHost(host)
	if !loopback && !opts.AllowRemote {
		return nil, opts, fmt.Errorf("拒绝绑定 %s：这不是本机地址。若确需让同网段其他设备访问书架，"+
			"请显式加 --allow-remote（等同授权外网访问，风险自负）", opts.Addr)
	}
	if opts.Token == "" {
		t, err := newToken()
		if err != nil {
			return nil, opts, err
		}
		opts.Token = t
	}
	allowedHosts := map[string]bool{"127.0.0.1": true, "localhost": true, "::1": true, "[::1]": true}
	if !loopback {
		allowedHosts[strings.ToLower(host)] = true
	}

	mux := http.NewServeMux()
	registerRoutes(mux, a, opts)

	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 1) Host 头校验：只认本机名/本机地址，阻断 DNS rebinding
		if !allowedHosts[strings.ToLower(stripPort(r.Host))] {
			http.Error(w, "非法 Host 头：本服务只接受本机访问", http.StatusForbidden)
			return
		}
		// 2) 令牌校验：?token= 首次进入即种下 HttpOnly Cookie
		if tok := r.URL.Query().Get("token"); tok != "" && tok == opts.Token {
			http.SetCookie(w, &http.Cookie{
				Name: cookieName, Value: opts.Token, Path: "/",
				HttpOnly: true, SameSite: http.SameSiteStrictMode,
			})
			if r.URL.Path == "/" { // 把令牌从地址栏/历史里去掉
				http.Redirect(w, r, "/", http.StatusFound)
				return
			}
		} else if c, err := r.Cookie(cookieName); err != nil || c.Value != opts.Token {
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			http.Error(w, "需要访问令牌：请用启动时打印的带 ?token= 的地址打开本页面", http.StatusUnauthorized)
			return
		}
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		mux.ServeHTTP(w, r)
	})
	return h, opts, nil
}

// Run 启动 HTTP 服务（阻塞直到进程退出）。
func Run(a *app.App, opts Options) error {
	h, opts, err := Handler(a, opts)
	if err != nil {
		return err
	}
	if !opts.Quiet {
		url := fmt.Sprintf("http://%s/?token=%s", opts.Addr, opts.Token)
		fmt.Println("拾书 · B/S 模式已启动")
		fmt.Printf("  访问地址（含令牌，请勿外传）：%s\n", url)
		if !isLoopbackHost(stripPort(opts.Addr)) {
			fmt.Println("  ⚠ 已授权非本机访问：同网段设备只要拿到上面这条地址就能看到你的书架。")
			fmt.Println("    本服务没有 TLS，请勿在不可信网络下使用；用完请关闭。")
		}
		if st := a.Status(); st.Root != "" {
			fmt.Printf("  当前库：%s（%d 本）\n", st.Root, st.Total)
		} else {
			fmt.Println("  尚未添加库：在页面左侧「＋ 添加库目录」里选一个目录")
		}
		fmt.Println("  Ctrl+C 退出")
	}
	srv := &http.Server{
		Addr:              opts.Addr,
		Handler:           h,
		ReadHeaderTimeout: 10 * time.Second,
	}
	return srv.ListenAndServe()
}

// URL 返回带令牌的访问地址（供桌面壳「在浏览器中打开」使用）。
func URL(addr, token string) string { return fmt.Sprintf("http://%s/?token=%s", addr, token) }

// NewToken 生成一个新的访问令牌。
func NewToken() (string, error) { return newToken() }

func newToken() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func isLoopbackHost(host string) bool {
	h := strings.ToLower(strings.Trim(host, "[]"))
	switch h {
	case "127.0.0.1", "localhost", "::1":
		return true
	case "", "0.0.0.0", "::":
		return false // 空/通配 = 所有网卡，属于对外暴露
	}
	if ip := net.ParseIP(h); ip != nil {
		return ip.IsLoopback()
	}
	return false
}

func stripPort(hostport string) string {
	if h, _, err := net.SplitHostPort(hostport); err == nil {
		return h
	}
	return hostport
}

func registerRoutes(mux *http.ServeMux, a *app.App, opts Options) {
	mux.HandleFunc("/thumb", func(w http.ResponseWriter, r *http.Request) {
		data, err := a.ThumbBytes(r.URL.Query().Get("id"))
		if err != nil {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "image/jpeg")
		w.Header().Set("Cache-Control", "max-age=86400")
		_, _ = w.Write(data)
	})

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(r.URL.Path, "/")
		if name == "" {
			name = "index.html"
		}
		data, err := readAsset(opts, name)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		writeAsset(w, name, data)
	})

	// 查询类
	get(mux, "/api/items", func() (any, error) { return a.Items(), nil })
	get(mux, "/api/shelves", func() (any, error) { return a.Shelves(), nil })
	get(mux, "/api/report", func() (any, error) { return a.Report(), nil })
	get(mux, "/api/status", func() (any, error) { return a.Status(), nil })
	get(mux, "/api/colors", func() (any, error) { return a.SpineColors(), nil })
	get(mux, "/api/libraries", func() (any, error) { return a.Libraries(), nil })
	get(mux, "/api/drives", func() (any, error) { return a.Drives(), nil })
	get(mux, "/api/version", func() (any, error) { return map[string]string{"version": app.Version}, nil })
	mux.HandleFunc("/api/settings", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			var s config.Settings
			if err := json.NewDecoder(r.Body).Decode(&s); err != nil {
				writeResult(w, nil, err)
				return
			}
			writeResult(w, map[string]bool{"ok": true}, a.SaveSettings(s))
			return
		}
		writeResult(w, a.Settings(), nil)
	})

	// 在线书目检索（只在用户点击时触发）
	mux.HandleFunc("/covercand", func(w http.ResponseWriter, r *http.Request) {
		data, err := a.CandidateCover(r.URL.Query().Get("id"))
		if err != nil {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "image/jpeg")
		w.Header().Set("Cache-Control", "max-age=3600")
		_, _ = w.Write(data)
	})
	post(mux, "/api/metadata/search", func(body json.RawMessage) (any, error) {
		var req struct {
			ID      string   `json:"id"`
			Query   string   `json:"query"`
			Sources []string `json:"sources"`
		}
		if err := json.Unmarshal(body, &req); err != nil {
			return nil, err
		}
		return a.SearchMetadata(req.ID, req.Query, req.Sources)
	})
	post(mux, "/api/metadata/apply", func(body json.RawMessage) (any, error) {
		var req struct {
			ID      string             `json:"id"`
			Cand    metadata.Candidate `json:"candidate"`
			Fields  []string           `json:"fields"`
		}
		if err := json.Unmarshal(body, &req); err != nil {
			return nil, err
		}
		return map[string]bool{"ok": true}, a.ApplyCandidate(req.ID, req.Cand, req.Fields)
	})

	// 封面（用户指定：粘贴 / 选文件 / 清除；B/S 模式下同样可用）
	mux.HandleFunc("/cover", func(w http.ResponseWriter, r *http.Request) {
		data, err := a.CoverBytes(r.URL.Query().Get("id"))
		if err != nil {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "image/jpeg")
		w.Header().Set("Cache-Control", "max-age=3600")
		_, _ = w.Write(data)
	})
	post(mux, "/api/cover/data", func(body json.RawMessage) (any, error) {
		var req struct {
			ID     string `json:"id"`
			Data   string `json:"data"`
			Source string `json:"source"`
		}
		if err := json.Unmarshal(body, &req); err != nil {
			return nil, err
		}
		return map[string]bool{"ok": true}, a.SetCoverFromData(req.ID, req.Data, req.Source)
	})
	post(mux, "/api/cover/file", func(body json.RawMessage) (any, error) {
		var req struct {
			ID   string `json:"id"`
			Path string `json:"path"`
		}
		if err := json.Unmarshal(body, &req); err != nil {
			return nil, err
		}
		return map[string]bool{"ok": true}, a.SetCoverFromFile(req.ID, req.Path)
	})
	post(mux, "/api/cover/clear", func(body json.RawMessage) (any, error) {
		var req struct {
			ID string `json:"id"`
		}
		_ = json.Unmarshal(body, &req)
		return map[string]bool{"ok": true}, a.ClearCover(req.ID)
	})

	// 动作类
	post(mux, "/api/scan", func(body json.RawMessage) (any, error) { return a.Scan() })
	post(mux, "/api/discover", func(body json.RawMessage) (any, error) {
		var o app.DiscoverOptions
		if len(body) > 0 {
			if err := json.Unmarshal(body, &o); err != nil {
				return nil, err
			}
		}
		return a.Discover(o)
	})
	post(mux, "/api/move", func(body json.RawMessage) (any, error) {
		var req struct {
			ID      string `json:"id"`
			ShelfID string `json:"shelfId"`
			Index   int    `json:"index"`
		}
		if err := json.Unmarshal(body, &req); err != nil {
			return nil, err
		}
		return map[string]bool{"ok": true}, a.MoveItem(req.ID, req.ShelfID, req.Index)
	})
	post(mux, "/api/shelf/create", func(body json.RawMessage) (any, error) {
		var req struct {
			Name string `json:"name"`
		}
		_ = json.Unmarshal(body, &req)
		return a.CreateShelf(req.Name)
	})
	post(mux, "/api/shelf/rename", func(body json.RawMessage) (any, error) {
		var req struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		}
		_ = json.Unmarshal(body, &req)
		return map[string]bool{"ok": true}, a.RenameShelf(req.ID, req.Name)
	})
	post(mux, "/api/shelf/delete", func(body json.RawMessage) (any, error) {
		var req struct {
			ID string `json:"id"`
		}
		_ = json.Unmarshal(body, &req)
		return map[string]bool{"ok": true}, a.DeleteShelf(req.ID)
	})
	post(mux, "/api/override", func(body json.RawMessage) (any, error) {
		var req struct {
			ID       string         `json:"id"`
			Override store.Override `json:"override"`
		}
		if err := json.Unmarshal(body, &req); err != nil {
			return nil, err
		}
		return map[string]bool{"ok": true}, a.SetOverride(req.ID, req.Override)
	})
	post(mux, "/api/library/add", func(body json.RawMessage) (any, error) {
		var req struct {
			Root string `json:"root"`
		}
		_ = json.Unmarshal(body, &req)
		return a.AddLibrary(req.Root)
	})
	post(mux, "/api/library/active", func(body json.RawMessage) (any, error) {
		var req struct {
			Root string `json:"root"`
		}
		_ = json.Unmarshal(body, &req)
		return map[string]bool{"ok": true}, a.SetActiveLibrary(req.Root)
	})
	post(mux, "/api/library/remove", func(body json.RawMessage) (any, error) {
		var req struct {
			Root string `json:"root"`
		}
		_ = json.Unmarshal(body, &req)
		return map[string]bool{"ok": true}, a.RemoveLibrary(req.Root)
	})
	post(mux, "/api/open", func(body json.RawMessage) (any, error) {
		var req struct {
			ID string `json:"id"`
		}
		_ = json.Unmarshal(body, &req)
		p, err := a.OpenFile(req.ID)
		return map[string]string{"path": p}, err
	})
	post(mux, "/api/reveal", func(body json.RawMessage) (any, error) {
		var req struct {
			ID string `json:"id"`
		}
		_ = json.Unmarshal(body, &req)
		p, err := a.RevealFile(req.ID)
		return map[string]string{"path": p}, err
	})
	post(mux, "/api/copy", func(body json.RawMessage) (any, error) {
		var req struct {
			ID string `json:"id"`
		}
		_ = json.Unmarshal(body, &req)
		p, err := a.CopyPath(req.ID)
		return map[string]string{"path": p}, err
	})
}

func readAsset(opts Options, name string) ([]byte, error) {
	if opts.Dev && opts.DistDir != "" {
		return os.ReadFile(filepath.Join(opts.DistDir, filepath.FromSlash(name)))
	}
	if opts.Dist == nil {
		return nil, fs.ErrNotExist
	}
	return fs.ReadFile(opts.Dist, name)
}

func get(mux *http.ServeMux, path string, fn func() (any, error)) {
	mux.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
		v, err := fn()
		writeResult(w, v, err)
	})
}

func post(mux *http.ServeMux, path string, fn func(json.RawMessage) (any, error)) {
	mux.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
		var body json.RawMessage
		if r.Body != nil {
			var raw json.RawMessage
			if err := json.NewDecoder(r.Body).Decode(&raw); err == nil {
				body = raw
			}
		}
		v, err := fn(body)
		writeResult(w, v, err)
	})
}

func writeResult(w http.ResponseWriter, v any, err error) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}
	_ = json.NewEncoder(w).Encode(v)
}

func writeAsset(w http.ResponseWriter, name string, data []byte) {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".html":
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
	case ".js":
		w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
	case ".css":
		w.Header().Set("Content-Type", "text/css; charset=utf-8")
	case ".svg":
		w.Header().Set("Content-Type", "image/svg+xml")
	}
	_, _ = w.Write(data)
}
