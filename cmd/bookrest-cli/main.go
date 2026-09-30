// Command bookrest-cli 是开发/调试工具（产品本体是 cmd/bookrest，它同时支持 C/S 窗口与 B/S 服务两种模式）。
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
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/RexKang/bookrest/internal/app"
	"github.com/RexKang/bookrest/internal/config"
	"github.com/RexKang/bookrest/internal/domain"
	"github.com/RexKang/bookrest/internal/report"
	"github.com/RexKang/bookrest/internal/server"
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
		// 与产品 B/S 模式共用同一份实现（internal/server）
		if err := server.Run(a, server.Options{Addr: *addr, DistDir: *distDir, Dev: true}); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
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
