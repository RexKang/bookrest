// Package server 是 B/S 传输：把应用层暴露成 HTTP + 静态资源，浏览器访问同一套前端。
// 桌面壳（Wails）与它共用 internal/app —— 这就是设计里「传输层可替换」的落点。
package server

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/RexKang/bookrest/internal/app"
	"github.com/RexKang/bookrest/internal/config"
	"github.com/RexKang/bookrest/internal/store"
)

type Options struct {
	Addr    string
	Dist    fs.FS  // 内嵌静态资源（生产模式）
	DistDir string // 磁盘静态资源目录（开发模式，改动即时可见）
	Dev     bool
}

// Run 启动 HTTP 服务（阻塞直到进程退出）。
func Run(a *app.App, opts Options) error {
	mux := http.NewServeMux()

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
	get(mux, "/api/status", func() (any, error) { return a.Status(), nil })
	get(mux, "/api/colors", func() (any, error) { return a.SpineColors(), nil })
	get(mux, "/api/libraries", func() (any, error) { return a.Libraries(), nil })
	get(mux, "/api/drives", func() (any, error) { return a.Drives(), nil })
	get(mux, "/api/version", func() (any, error) { return map[string]string{"version": app.Version}, nil })

	// 动作类
	post(mux, "/api/scan", func(body json.RawMessage) (any, error) { return a.Scan() })
	post(mux, "/api/discover", func(body json.RawMessage) (any, error) {
		var opts app.DiscoverOptions
		if len(body) > 0 {
			if err := json.Unmarshal(body, &opts); err != nil {
				return nil, err
			}
		}
		return a.Discover(opts)
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

	fmt.Printf("拾书 · B/S 模式已启动：http://%s\n", opts.Addr)
	if st := a.Status(); st.Root != "" {
		fmt.Printf("  当前库：%s（%d 本）\n", st.Root, st.Total)
	} else {
		fmt.Println("  尚未添加库：在页面左侧「＋ 添加库目录」里选一个目录")
	}
	fmt.Println("  Ctrl+C 退出")

	return http.ListenAndServe(opts.Addr, mux)
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
