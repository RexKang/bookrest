// Command bookrest 是拾书桌面应用（Wails v2 外壳 + 内嵌前端）。
//
// 设计要点（见 docs/260929-拾书-详细设计-v0.1.0.md）：
//   - 外壳只做「窗口 + 事件 + 资源」三件事，业务全在 internal/app
//   - 缩略图通过资源处理器的 /thumb?id=<id> 提供，按需生成并缓存
//   - 前端静态资源内嵌，无需 npm/构建步骤
package main

import (
	"context"
	"log"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/RexKang/bookrest/frontend"
	"github.com/RexKang/bookrest/internal/app"
	"github.com/RexKang/bookrest/internal/config"
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
	shell := &Shell{}

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
	ctx context.Context
}

// PickLibraryDir 打开系统目录选择器，返回用户选择的库根目录。
func (s *Shell) PickLibraryDir() (string, error) {
	return runtime.OpenDirectoryDialog(s.ctx, runtime.OpenDialogOptions{
		Title: "选择要拾起的书库目录（拾书不会导入或移动文件）",
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
