// Command bookrest 是拾书桌面应用，同一份内核与前端支持两种模式：
//
//	C/S（默认）：Wails v2 原生窗口 + 内嵌前端
//	B/S：bookrest.exe --serve [--addr 127.0.0.1:8788]，浏览器访问同一套界面
//
// 设计要点（见 docs/260929-拾书-详细设计-v0.1.0.md）：
//   - 外壳只做「窗口 + 事件 + 资源」三件事，业务全在 internal/app
//   - 缩略图通过资源处理器的 /thumb?id=<id> 提供，按需生成并缓存
//   - 前端静态资源内嵌，无需 npm/构建步骤
package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/RexKang/bookrest/frontend"
	"github.com/RexKang/bookrest/internal/app"
	"github.com/RexKang/bookrest/internal/config"
	"github.com/RexKang/bookrest/internal/server"
	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

func main() {
	cfgPath := filepath.Join(config.AppDir(), "config.json")
	a, err := app.New(cfgPath, nil)
	if err != nil {
		log.Fatal("初始化失败: ", err)
	}
	shell := &Shell{app: a}

	// 命令行开关
	addr := "127.0.0.1:8788"
	serveMode := false
	allowRemote := false
	for i, arg := range os.Args[1:] {
		switch arg {
		case "--serve", "-s", "serve":
			serveMode = true
		case "--allow-remote":
			allowRemote = true
		case "--version", "-v":
			fmt.Printf("拾书 Bookrest %s\n", app.Version)
			return
		case "--addr":
			if i+2 < len(os.Args) {
				addr = os.Args[i+2]
			}
		case "--help", "-h":
			fmt.Println("拾书 Bookrest " + app.Version)
			fmt.Println("  （无参数）            打开桌面窗口（C/S）")
			fmt.Println("  --serve [--addr 地址] 以 B/S 模式启动本地服务，浏览器访问（默认只绑本机）")
			fmt.Println("  --allow-remote        显式授权绑定非本机地址（局域网访问；风险自负）")
			fmt.Println("  --version             显示版本号")
			return
		}
	}

	// B/S 模式：同一份内核与前端，换 HTTP 传输
	// 安全：默认只绑本机回环地址 + 随机访问令牌；绑定非本机地址必须显式 --allow-remote
	if serveMode {
		if err := server.Run(a, server.Options{
			Addr: addr, Dist: frontend.Assets(), AllowRemote: allowRemote,
		}); err != nil {
			log.Fatal(err)
		}
		return
	}

	err = wails.Run(&options.App{
		Title:     "拾书 Bookrest",
		Width:     1280,
		Height:    820,
		MinWidth:  980,
		MinHeight: 620,
		AssetServer: &assetserver.Options{
			Assets:  frontend.Assets(),
			Handler: http.HandlerFunc(thumbHandler(a)),
		},
		OnStartup: func(ctx context.Context) {
			shell.ctx = ctx
			a.SetEmitter(func(event string, data any) {
				runtime.EventsEmit(ctx, event, data)
			})
		},
		Bind: []interface{}{a, shell},
	})
	if err != nil {
		log.Fatal(err)
	}
}

// Shell 是外壳专有能力的绑定（需要 Wails 上下文的东西都放这里）。
type Shell struct {
	ctx     context.Context
	app     *app.App
	webOnce sync.Once
	webURL  string
	webErr  error
}

// OpenInBrowser 在进程内起一个 B/S 服务（只绑本机 + 随机令牌），用系统浏览器打开。
// 令牌随 URL 一起给出，浏览器首次进入后转存为 HttpOnly Cookie，地址栏不留令牌。
func (s *Shell) OpenInBrowser() (string, error) {
	const addr = "127.0.0.1:8788"
	s.webOnce.Do(func() {
		tok, err := server.NewToken()
		if err != nil {
			s.webErr = err
			return
		}
		s.webURL = server.URL(addr, tok)
		go func() {
			if err := server.Run(s.app, server.Options{
				Addr: addr, Dist: frontend.Assets(), Token: tok, Quiet: true,
			}); err != nil {
				log.Println("B/S 模式启动失败:", err)
			}
		}()
		time.Sleep(250 * time.Millisecond)
	})
	if s.webErr != nil {
		return "", s.webErr
	}
	runtime.BrowserOpenURL(s.ctx, s.webURL)
	return s.webURL, nil
}

// PickLibraryDir 打开系统目录选择器，返回用户选择的库根目录。
func (s *Shell) PickLibraryDir() (string, error) {
	return runtime.OpenDirectoryDialog(s.ctx, runtime.OpenDialogOptions{
		Title: "选择要拾起的书库目录（拾书不会导入或移动文件）",
	})
}

// PickImageFile 打开图片选择器（用于手动指定封面）。
func (s *Shell) PickImageFile() (string, error) {
	return runtime.OpenFileDialog(s.ctx, runtime.OpenDialogOptions{
		Title: "选择封面图片",
		Filters: []runtime.FileFilter{
			{DisplayName: "图片 (*.jpg;*.jpeg;*.png;*.gif;*.webp)", Pattern: "*.jpg;*.jpeg;*.png;*.gif;*.webp"},
		},
	})
}

// thumbHandler 处理 GET /thumb?id=<id>：按需生成封面缩略图（其余请求交给内嵌资源）。
func thumbHandler(a *app.App) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/thumb") {
			http.NotFound(w, r)
			return
		}
		data, err := a.ThumbBytes(r.URL.Query().Get("id"))
		if err != nil {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "image/jpeg")
		w.Header().Set("Cache-Control", "max-age=86400")
		_, _ = w.Write(data)
	}
}
