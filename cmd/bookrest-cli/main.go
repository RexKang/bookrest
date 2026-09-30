// Command bookrest-cli 是开发/调试工具（产品本体是 cmd/bookrest 桌面应用）。
//
//	bookrest-cli scan   --root <库根>
//	bookrest-cli shelf  --root <库根> [--series <系列>]
//	bookrest-cli report --root <库根> [--json]
//	bookrest-cli serve  --root <库根> [--addr 127.0.0.1:8899]   # 用真前端做无头冒烟
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/RexKang/bookrest/internal/app"
	"github.com/RexKang/bookrest/internal/config"
	"github.com/RexKang/bookrest/internal/domain"
	"github.com/RexKang/bookrest/internal/report"
	"github.com/RexKang/bookrest/internal/store"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "用法：bookrest-cli <scan|shelf|report|serve> --root <库根>")
		os.Exit(2)
	}
	cmd := os.Args[1]
	fs := flag.NewFlagSet(cmd, flag.ExitOnError)
	root := fs.String("root", "", "库根目录")
	cfgPath := fs.String("config", filepath.Join(config.AppDir(), "config.json"), "配置文件路径")
	series := fs.String("series", "", "只看某个系列")
	width := fs.Int("width", 100, "终端宽度")
	asJSON := fs.Bool("json", false, "输出 JSON")
	addr := fs.String("addr", "127.0.0.1:8899", "serve 监听地址")
	distDir := fs.String("dist", "frontend/dist", "前端静态目录（serve 用）")
	_ = fs.Parse(os.Args[2:])
	if *root == "" {
		fmt.Fprintln(os.Stderr, "必须指定 --root")
		os.Exit(2)
	}

	a, err := app.New(*cfgPath, nil)
	if err != nil {
		fmt.Fprintln(os.Stderr, "初始化失败:", err)
		os.Exit(1)
	}
	if _, err := a.AddLibrary(*root); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	switch cmd {
	case "scan":
		sum, err := a.Scan()
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		fmt.Printf("扫描完成：%d 本在库（用时 %dms）\n", sum.Total, sum.Millis)
		fmt.Printf("  新增 %d / 更新 %d / 移动 %d / 重复 %d / 缺失 %d\n", sum.Added, sum.Updated, sum.Moved, sum.Duplicates, sum.Missing)
		fmt.Printf("  成本：剪枝目录 %d / 文件 stat %d / 哈希 %d / 解析 %d\n", sum.PrunedDirs, sum.FilesStat, sum.FilesHash, sum.Parsed)
	case "shelf":
		cmdShelf(a, *series, *width)
	case "report":
		cmdReport(a, *asJSON)
	case "serve":
		cmdServe(a, *addr, *distDir)
	default:
		fmt.Fprintln(os.Stderr, "未知子命令:", cmd)
		os.Exit(2)
	}
}

func cmdShelf(a *app.App, only string, width int) {
	items := a.Items()
	colors := a.SpineColors()
	groups := map[string][]app.Item{}
	for _, it := range items {
		if it.Missing {
			continue
		}
		name := it.Series
		if name == "" {
			name = "(未识别系列)"
		}
		groups[name] = append(groups[name], it)
	}
	var names []string
	for n := range groups {
		names = append(names, n)
	}
	sort.Strings(names)
	total := 0
	for _, n := range names {
		total += len(groups[n])
	}
	fmt.Printf("书架  共 %d 本 / %d 个系列\n\n", total, len(names))
	for _, n := range names {
		if only != "" && n != only {
			continue
		}
		books := groups[n]
		sort.Slice(books, func(i, j int) bool {
			ni, oki := domain.NumberValue(books[i].Number)
			nj, okj := domain.NumberValue(books[j].Number)
			if oki && okj && ni != nj {
				return ni < nj
			}
			return books[i].Rel < books[j].Rel
		})
		fmt.Printf("── %s（%d 卷）\n", n, len(books))
		var blocks, labels strings.Builder
		line := 0
		for _, b := range books {
			w := 3
			if b.Pages > 0 {
				w = 2 + b.Pages/40
				if w > 8 {
					w = 8
				}
			}
			blocks.WriteString(ansiBG(colors[b.ID]) + strings.Repeat(" ", w) + "\x1b[0m ")
			num := b.Number
			if num == "" {
				num = "·"
			}
			labels.WriteString(pad(num, w) + " ")
			line += w + 1
			if line > width-10 {
				fmt.Println(blocks.String())
				fmt.Println(labels.String())
				blocks.Reset()
				labels.Reset()
				line = 0
			}
		}
		if blocks.Len() > 0 {
			fmt.Println(blocks.String())
			fmt.Println(labels.String())
		}
		fmt.Println()
	}
}

func ansiBG(hex string) string {
	var r, g, b int
	if _, err := fmt.Sscanf(strings.TrimPrefix(hex, "#"), "%02x%02x%02x", &r, &g, &b); err != nil {
		return "\x1b[48;2;80;80;80m"
	}
	return fmt.Sprintf("\x1b[48;2;%d;%d;%dm", r, g, b)
}

func pad(s string, w int) string {
	n := len([]rune(s))
	if n >= w {
		return s
	}
	left := (w - n) / 2
	return strings.Repeat(" ", left) + s + strings.Repeat(" ", w-n-left)
}

func cmdReport(a *app.App, asJSON bool) {
	rep := a.Report()
	if asJSON {
		data, _ := json.MarshalIndent(rep, "", "  ")
		fmt.Println(string(data))
		return
	}
	fmt.Printf("体检报告  共 %d 本（只读分析，未修改任何文件）\n\n", rep.Total)
	if len(rep.Findings) == 0 {
		fmt.Println("未发现问题")
		return
	}
	for _, f := range rep.Findings {
		switch f.Kind {
		case report.KindGap:
			fmt.Printf("[缺卷] %s：缺 %s（现有 %d 卷）\n", f.Series, report.NumberString(f.Missing), len(f.Items))
		case report.KindDup:
			fmt.Printf("[重复] %s\n", strings.Join(f.Items, " ≡ "))
		default:
			fmt.Printf("[%s] %d 个（%s）\n", f.Kind, len(f.Items), f.Detail)
			for i, it := range f.Items {
				if i == 5 {
					fmt.Printf("       … 其余 %d 个\n", len(f.Items)-5)
					break
				}
				fmt.Printf("       %s\n", it)
			}
		}
	}
}

// cmdServe 用真前端 + HTTP 传输跑无头冒烟（产品走 Wails 绑定）。
func cmdServe(a *app.App, addr, dist string) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path
		if p == "/" {
			p = "/index.html"
		}
		data, err := os.ReadFile(filepath.Join(dist, filepath.FromSlash(strings.TrimPrefix(p, "/"))))
		if err != nil {
			http.NotFound(w, r)
			return
		}
		switch filepath.Ext(p) {
		case ".html":
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
		case ".js":
			w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
		case ".css":
			w.Header().Set("Content-Type", "text/css; charset=utf-8")
		}
		_, _ = w.Write(data)
	})
	mux.HandleFunc("/thumb", func(w http.ResponseWriter, r *http.Request) {
		data, err := a.ThumbBytes(r.URL.Query().Get("id"))
		if err != nil {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "image/jpeg")
		_, _ = w.Write(data)
	})
	jsonEP := func(path string, fn func() (any, error)) {
		mux.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
			v, err := fn()
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			if err != nil {
				w.WriteHeader(http.StatusBadRequest)
				_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
				return
			}
			_ = json.NewEncoder(w).Encode(v)
		})
	}
	jsonEP("/api/items", func() (any, error) { return a.Items(), nil })
	jsonEP("/api/shelves", func() (any, error) { return a.Shelves(), nil })
	jsonEP("/api/report", func() (any, error) { return a.Report(), nil })
	jsonEP("/api/status", func() (any, error) { return a.Status(), nil })
	jsonEP("/api/colors", func() (any, error) { return a.SpineColors(), nil })
	jsonEP("/api/libraries", func() (any, error) { return a.Libraries(), nil })
	mux.HandleFunc("/api/scan", func(w http.ResponseWriter, r *http.Request) {
		sum, err := a.Scan()
		writeResult(w, sum, err)
	})
	mux.HandleFunc("/api/move", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			ID      string `json:"id"`
			ShelfID string `json:"shelfId"`
			Index   int    `json:"index"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		writeResult(w, map[string]bool{"ok": true}, a.MoveItem(req.ID, req.ShelfID, req.Index))
	})
	mux.HandleFunc("/api/shelf/create", func(w http.ResponseWriter, r *http.Request) {
		var req struct{ Name string `json:"name"` }
		_ = json.NewDecoder(r.Body).Decode(&req)
		sh, err := a.CreateShelf(req.Name)
		writeResult(w, sh, err)
	})
	mux.HandleFunc("/api/override", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			ID       string         `json:"id"`
			Override store.Override `json:"override"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		writeResult(w, map[string]bool{"ok": true}, a.SetOverride(req.ID, req.Override))
	})
	mux.HandleFunc("/api/open", func(w http.ResponseWriter, r *http.Request) {
		var req struct{ ID string `json:"id"` }
		_ = json.NewDecoder(r.Body).Decode(&req)
		p, err := a.OpenFile(req.ID)
		writeResult(w, map[string]string{"path": p, "hint": "dev 模式已尝试调用系统默认程序"}, err)
	})
	mux.HandleFunc("/api/reveal", func(w http.ResponseWriter, r *http.Request) {
		var req struct{ ID string `json:"id"` }
		_ = json.NewDecoder(r.Body).Decode(&req)
		p, err := a.RevealFile(req.ID)
		writeResult(w, map[string]string{"path": p}, err)
	})
	mux.HandleFunc("/api/copy", func(w http.ResponseWriter, r *http.Request) {
		var req struct{ ID string `json:"id"` }
		_ = json.NewDecoder(r.Body).Decode(&req)
		p, err := a.CopyPath(req.ID)
		writeResult(w, map[string]string{"path": p}, err)
	})
	mux.HandleFunc("/api/shelf/rename", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		writeResult(w, map[string]bool{"ok": true}, a.RenameShelf(req.ID, req.Name))
	})
	mux.HandleFunc("/api/shelf/delete", func(w http.ResponseWriter, r *http.Request) {
		var req struct{ ID string `json:"id"` }
		_ = json.NewDecoder(r.Body).Decode(&req)
		writeResult(w, map[string]bool{"ok": true}, a.DeleteShelf(req.ID))
	})
	mux.HandleFunc("/api/settings", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			var s config.Settings
			_ = json.NewDecoder(r.Body).Decode(&s)
			writeResult(w, map[string]bool{"ok": true}, a.SaveSettings(s))
			return
		}
		writeResult(w, a.Settings(), nil)
	})
	mux.HandleFunc("/api/library/add", func(w http.ResponseWriter, r *http.Request) {
		var req struct{ Root string `json:"root"` }
		_ = json.NewDecoder(r.Body).Decode(&req)
		st, err := a.AddLibrary(req.Root)
		writeResult(w, st, err)
	})
	mux.HandleFunc("/api/library/active", func(w http.ResponseWriter, r *http.Request) {
		var req struct{ Root string `json:"root"` }
		_ = json.NewDecoder(r.Body).Decode(&req)
		writeResult(w, map[string]bool{"ok": true}, a.SetActiveLibrary(req.Root))
	})
	mux.HandleFunc("/api/library/remove", func(w http.ResponseWriter, r *http.Request) {
		var req struct{ Root string `json:"root"` }
		_ = json.NewDecoder(r.Body).Decode(&req)
		writeResult(w, map[string]bool{"ok": true}, a.RemoveLibrary(req.Root))
	})
	fmt.Printf("开发服务器：http://%s（真前端 + HTTP 传输，产品本体是 Wails 桌面应用）\n", addr)
	log.Fatal(http.ListenAndServe(addr, mux))
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
