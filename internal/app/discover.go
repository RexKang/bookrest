package app

import (
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/RexKang/bookrest/internal/config"
	"github.com/RexKang/bookrest/internal/parse"
)

// ---------- 全盘扫描（找书，不动任何文件） ----------

type DriveInfo struct {
	Letter   string `json:"letter"`   // "D:"
	Path     string `json:"path"`     // "D:\\"
	Readable bool   `json:"readable"`
	HasBooks bool   `json:"hasBooks"` // 已注册为库
}

// Drives 列出本机可读盘符（A–Z 探测，只读判断，不写盘）。
func (a *App) Drives() []DriveInfo {
	a.mu.Lock()
	libs := append([]config.Library(nil), a.cfg.Libraries...)
	a.mu.Unlock()

	var out []DriveInfo
	for c := 'A'; c <= 'Z'; c++ {
		root := string(c) + ":\\"
		fi, err := os.Stat(root)
		if err != nil || !fi.IsDir() {
			continue
		}
		d := DriveInfo{Letter: string(c) + ":", Path: root, Readable: true}
		for _, l := range libs {
			if len(l.Root) >= 2 && strings.EqualFold(l.Root[:2], d.Letter) {
				d.HasBooks = true
			}
		}
		out = append(out, d)
	}
	return out
}

// DiscoverOptions 控制一次全盘扫描。
type DiscoverOptions struct {
	Drives        []string `json:"drives"`        // 参与扫描的盘符，如 ["D:","E:"]
	Extensions    []string `json:"extensions"`    // 关注的文件类型，如 [".epub",".pdf"]
	ExcludeCommon bool     `json:"excludeCommon"` // 屏蔽常见目录（系统/开发/缓存）
	ExtraExcludes []string `json:"extraExcludes"` // 额外排除的目录名
	MaxDepth      int      `json:"maxDepth"`      // 最大目录深度（默认 6）
	MinBooks      int      `json:"minBooks"`      // 命中多少本才算候选库（默认 5）
	MaxSeconds    int      `json:"maxSeconds"`    // 时间上限（默认 120）
}

type DiscoverHit struct {
	Dir   string         `json:"dir"`
	Books int            `json:"books"`
	Exts  map[string]int `json:"exts"`
}

type DiscoverResult struct {
	Hits         []DiscoverHit `json:"hits"`
	ScannedDirs  int           `json:"scannedDirs"`
	ScannedFiles int           `json:"scannedFiles"`
	ElapsedMS    int64         `json:"elapsedMs"`
	Truncated    bool          `json:"truncated"` // 因时间/深度上限提前结束
}

// commonExcludes 是「常见目录」屏蔽名单（按名字匹配，不区分大小写）。
var commonExcludes = map[string]bool{
	// 系统
	"windows": true, "program files": true, "program files (x86)": true, "programdata": true,
	"$recycle.bin": true, "system volume information": true, "recovery": true, "$winreagent": true,
	"perflogs": true, "driverstore": true, "winsxs": true, "msocache": true, "intel": true, "amd": true,
	// 开发
	"node_modules": true, ".git": true, ".svn": true, ".hg": true, "__pycache__": true, "site-packages": true,
	"vendor": true, "target": true, "dist": true, "build": true, "out": true, ".venv": true, "venv": true,
	".idea": true, ".vscode": true, ".gradle": true, ".cargo": true, "go": true, "pkg": true,
	// 缓存与临时
	"cache": true, ".cache": true, "temp": true, "tmp": true, "logs": true, "appdata": true,
	"onedrive": false, // 网盘目录里可能有书，默认不屏蔽
}

// Discover 全盘扫描找书：只读遍历，按目录聚合，返回候选库目录。
// 绝不写盘、不跟随符号链接、不进入屏蔽目录。
func (a *App) Discover(opts DiscoverOptions) (DiscoverResult, error) {
	start := time.Now()
	if opts.MaxDepth <= 0 {
		opts.MaxDepth = 6
	}
	if opts.MinBooks <= 0 {
		opts.MinBooks = 5
	}
	if opts.MaxSeconds <= 0 {
		opts.MaxSeconds = 120
	}
	want := map[string]bool{}
	for _, e := range opts.Extensions {
		if !strings.HasPrefix(e, ".") {
			e = "." + e
		}
		want[strings.ToLower(e)] = true
	}
	if len(want) == 0 {
		for _, e := range []string{".epub", ".pdf", ".cbz", ".cbr", ".mobi", ".azw3", ".fb2"} {
			want[e] = true
		}
	}
	extra := map[string]bool{}
	for _, e := range opts.ExtraExcludes {
		extra[strings.ToLower(strings.TrimSpace(e))] = true
	}

	res := DiscoverResult{}
	perDir := map[string]*DiscoverHit{}
	deadline := start.Add(time.Duration(opts.MaxSeconds) * time.Second)

	for _, d := range opts.Drives {
		root := strings.TrimSpace(d)
		sep := string(filepath.Separator)
		if !strings.HasSuffix(root, sep) && !strings.HasSuffix(root, "/") {
			root += sep
		}
		if fi, err := os.Stat(root); err != nil || !fi.IsDir() {
			continue
		}
		if time.Now().After(deadline) {
			res.Truncated = true
			break
		}
		_ = filepath.WalkDir(root, func(path string, de fs.DirEntry, err error) error {
			if err != nil {
				return nil // 无权限目录跳过，不中断
			}
			if time.Now().After(deadline) {
				res.Truncated = true
				return filepath.SkipAll
			}
			name := strings.ToLower(de.Name())
			if de.IsDir() {
				if path == root {
					return nil
				}
				depth := strings.Count(strings.TrimPrefix(path, root), string(filepath.Separator)) + 1
				if depth > opts.MaxDepth {
					return filepath.SkipDir
				}
				if opts.ExcludeCommon && commonExcludes[name] {
					return filepath.SkipDir
				}
				if extra[name] {
					return filepath.SkipDir
				}
				if strings.HasPrefix(de.Name(), "$") || name == "system volume information" {
					return filepath.SkipDir
				}
				res.ScannedDirs++
				if res.ScannedDirs%2000 == 0 {
					a.fire("discover:progress", map[string]any{
						"dirs": res.ScannedDirs, "files": res.ScannedFiles, "current": path,
					})
				}
				return nil
			}
			ext := strings.ToLower(filepath.Ext(de.Name()))
			if !want[ext] || !parse.IsBookExt(ext) {
				return nil
			}
			res.ScannedFiles++
			dir := filepath.Dir(path)
			h := perDir[dir]
			if h == nil {
				h = &DiscoverHit{Dir: filepath.ToSlash(dir), Exts: map[string]int{}}
				perDir[dir] = h
			}
			h.Books++
			h.Exts[ext]++
			return nil
		})
	}

	// 向上合并：父目录书籍数包含子目录（同一系列常分目录存放）
	for dir, h := range perDir {
		if h.Books < opts.MinBooks {
			continue
		}
		res.Hits = append(res.Hits, *h)
		_ = dir
	}
	sort.Slice(res.Hits, func(i, j int) bool { return res.Hits[i].Books > res.Hits[j].Books })
	if len(res.Hits) > 50 {
		res.Hits = res.Hits[:50]
	}
	res.ElapsedMS = time.Since(start).Milliseconds()
	a.fire("discover:done", map[string]any{"hits": len(res.Hits), "files": res.ScannedFiles, "ms": res.ElapsedMS})
	return res, nil
}
