// Package app 是应用层：外壳无关的服务集合（Wails 绑定、HTTP、CLI 都调它）。
// 职责：库注册与切换、扫描、查询、摆放/覆盖、体检、设置、系统集成、缩略图。
package app

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/RexKang/bookrest/internal/config"
	"github.com/RexKang/bookrest/internal/domain"
	"github.com/RexKang/bookrest/internal/metadata"
	"github.com/RexKang/bookrest/internal/parse"
	"github.com/RexKang/bookrest/internal/report"
	"github.com/RexKang/bookrest/internal/scan"
	"github.com/RexKang/bookrest/internal/store"
	"github.com/RexKang/bookrest/internal/thumb"
)

type Emit func(event string, data any)

type App struct {
	mu    sync.Mutex
	cfg   *config.Config
	cfgFn string

	idx   *store.Index
	shelf *store.Shelf
	state *store.ScanState
	root  string

	colors map[string]string
	thumbs map[string][]byte

	emit Emit
}

// Item 是给界面用的条目视图（事实 + 用户覆盖）。
type Item struct {
	ID      string   `json:"id"`
	Rel     string   `json:"rel"`
	Title   string   `json:"title"`
	Series  string   `json:"series"`
	Number  string   `json:"number"`
	Author  string   `json:"author"`
	Pages   int      `json:"pages"`
	Ext     string   `json:"ext"`
	Color   string   `json:"color"`
	Tags    []string `json:"tags"`
	Rating  int      `json:"rating"`
	Failed  bool     `json:"failed"`
	Missing bool     `json:"missing"`
	ShelfID string   `json:"shelfId"`
	Index   int      `json:"index"`
	// 封面来源：用户指定的封面会覆盖文件内嵌封面
	HasCover    bool   `json:"hasCover"`
	CoverSource string `json:"coverSource,omitempty"` // 手动粘贴 / 本地文件 / 站点名
}

type ScanSummary struct {
	Total      int `json:"total"`
	Added      int `json:"added"`
	Updated    int `json:"updated"`
	Moved      int `json:"moved"`
	Duplicates int `json:"duplicates"`
	Missing    int `json:"missing"`
	PrunedDirs int `json:"prunedDirs"`
	FilesStat  int `json:"filesStat"`
	FilesHash  int `json:"filesHash"`
	Parsed     int `json:"parsed"`
	Millis     int64 `json:"millis"`
}

type LibraryStatus struct {
	Root   string `json:"root"`
	Name   string `json:"name"`
	Online bool   `json:"online"`
	Hint   string `json:"hint"`
	Total  int    `json:"total"`
}

func New(cfgPath string, emit Emit) (*App, error) {
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return nil, err
	}
	a := &App{cfg: cfg, cfgFn: cfgPath, emit: emit, colors: map[string]string{}, thumbs: map[string][]byte{}}
	// 没有库时也要有空的索引/意图/扫描状态：任何查询路径都不允许出现空指针
	// （B/S 模式下 /api/items 之类的请求可以来自浏览器，崩一次就等于服务停摆）
	a.idx = store.NewIndex(filepath.Join(cfg.Settings.MirrorDir, "empty", "index.jsonl"))
	a.shelf = store.NewShelf("local")
	a.state = store.NewScanState("")
	if err := os.MkdirAll(cfg.Settings.MirrorDir, 0o755); err != nil {
		return nil, err
	}
	if cfg.Active != "" {
		if err := a.loadLibrary(cfg.Active); err != nil {
			return nil, err
		}
	}
	metadata.Configure(cfg.Settings.Proxy) // 联网补全信息时按设置走代理
	return a, nil
}

func (a *App) mirrorPath(name string) string {
	return filepath.Join(a.cfg.Settings.MirrorDir, name)
}

// loadLibrary 载入某库的镜像（索引/意图/扫描状态）。不访问库本体。
func (a *App) loadLibrary(root string) error {
	key := libKey(root)
	dir := filepath.Join(a.cfg.Settings.MirrorDir, key)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	idx := store.NewIndex(filepath.Join(dir, "index.jsonl"))
	if err := idx.Load(); err != nil {
		_ = idx.Save() // 索引可重建：损坏就直接重来
	}
	shelf, _, err := store.LoadShelf(filepath.Join(dir, "shelf.json"), filepath.Join(dir, "backups"))
	if err != nil {
		shelf = store.NewShelf("local")
	}
	st, err := store.LoadScanState(filepath.Join(dir, "scan_state.json"), root)
	if err != nil {
		return err
	}
	a.idx, a.shelf, a.state, a.root = idx, shelf, st, root
	a.colors = map[string]string{}
	a.thumbs = map[string][]byte{}
	return nil
}

// loadLibraryMirror 按当前设置重建镜像目录（设置变更后调用）。
func (a *App) loadLibraryMirror() error {
	if a.root == "" {
		return nil
	}
	return a.loadLibrary(a.root)
}

func libKey(root string) string {
	h := domain.UnstableID(filepath.Clean(root), 0, 0)
	return h
}

func (a *App) shelfDir() string {
	return filepath.Join(a.cfg.Settings.MirrorDir, libKey(a.root))
}

// ---------- 库管理 ----------

func (a *App) Libraries() []LibraryStatus {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([]LibraryStatus, 0, len(a.cfg.Libraries))
	for _, l := range a.cfg.Libraries {
		st := LibraryStatus{Root: l.Root, Name: l.Name}
		if dirOnline(l.Root) {
			st.Online = true
		} else {
			st.Hint = "目录不存在或未挂载"
		}
		if samePath(l.Root, a.root) {
			st.Total = a.idx.Len()
		}
		out = append(out, st)
	}
	return out
}

func (a *App) AddLibrary(root string) (LibraryStatus, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	fi, err := os.Stat(root)
	if err != nil || !fi.IsDir() {
		return LibraryStatus{}, fmt.Errorf("目录不存在：%s", root)
	}
	name := filepath.Base(filepath.Clean(root))
	a.cfg.AddLibrary(root, name)
	a.cfg.Active = root
	if err := a.cfg.Save(a.cfgFn); err != nil {
		return LibraryStatus{}, err
	}
	if err := a.loadLibrary(root); err != nil {
		return LibraryStatus{}, err
	}
	return LibraryStatus{Root: root, Name: name, Online: true}, nil
}

func (a *App) SetActiveLibrary(root string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.cfg.Active = root
	if err := a.cfg.Save(a.cfgFn); err != nil {
		return err
	}
	return a.loadLibrary(root)
}

func (a *App) RemoveLibrary(root string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.cfg.RemoveLibrary(root)
	return a.cfg.Save(a.cfgFn)
}

// Status 返回当前库状态（供离线横幅使用）。
func (a *App) Status() LibraryStatus {
	a.mu.Lock()
	defer a.mu.Unlock()
	st := LibraryStatus{Root: a.root, Total: a.idx.Len()}
	for _, l := range a.cfg.Libraries {
		if samePath(l.Root, a.root) {
			st.Name = l.Name
		}
	}
	if dirOnline(a.root) {
		st.Online = true
	} else {
		st.Hint = "目录不存在：库离线，界面进入只读模式"
	}
	return st
}

// dirOnline 判断库目录当前是否可达（离线只读模式的第一道判据）。
func dirOnline(root string) bool {
	if root == "" {
		return false
	}
	fi, err := os.Stat(root)
	return err == nil && fi.IsDir()
}

// ---------- 扫描 ----------

func (a *App) Scan() (ScanSummary, error) {
	a.mu.Lock()
	root, idx, st := a.root, a.idx, a.state
	a.mu.Unlock()
	if !dirOnline(root) {
		return ScanSummary{}, fmt.Errorf("库离线，无法扫描：%s", root)
	}

	a.fire("scan:start", map[string]string{"root": root})
	start := time.Now()
	sc := &scan.Scanner{Root: root}
	diff, err := sc.ScanIncremental(idx, st)
	if err != nil {
		a.fire("scan:error", map[string]string{"error": err.Error()})
		return ScanSummary{}, err
	}
	sum := ScanSummary{
		Total: idx.Len(), Added: len(diff.Added), Updated: len(diff.Updated), Moved: len(diff.Moved),
		Duplicates: len(diff.Duplicates), Missing: len(diff.Missing),
		PrunedDirs: sc.Stats.PrunedDirs, FilesStat: sc.Stats.FilesStatted,
		FilesHash: sc.Stats.FilesHashed, Parsed: sc.Stats.Parsed,
		Millis: time.Since(start).Milliseconds(),
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := idx.Save(); err != nil {
		return sum, err
	}
	if err := st.Save(filepath.Join(a.shelfDir(), "scan_state.json")); err != nil {
		return sum, err
	}
	a.colors = map[string]string{}
	a.fire("scan:done", sum)
	return sum, nil
}

// ---------- 查询 ----------

func (a *App) Items() []Item {
	a.mu.Lock()
	defer a.mu.Unlock()
	pos := map[string][2]any{}
	for _, sh := range a.shelf.Shelves {
		for i, id := range sh.Order {
			pos[id] = [2]any{sh.ID, i}
		}
	}
	items := make([]Item, 0, a.idx.Len())
	a.idx.Each(func(f store.Fact) bool {
		ov := a.shelf.Overrides[f.ID]
		if ov.Deleted {
			return true
		}
		it := Item{
			ID: f.ID, Rel: f.Rel, Ext: f.Ext, Failed: f.Art.Failed, Missing: f.Flags.Missing,
			Title: f.Meta.Title, Series: f.Meta.Series, Number: f.Meta.Number, Author: f.Meta.Author,
			Tags: ov.Tags, Rating: ov.Rating,
		}
		if ov.Title != "" {
			it.Title = ov.Title
		}
		if ov.Series != "" {
			it.Series = ov.Series
		}
		if ov.Number != "" {
			it.Number = ov.Number
		}
		if ov.Author != "" {
			it.Author = ov.Author
		}
		if ov.Cover != nil {
			it.HasCover = true
			it.CoverSource = ov.Cover.Source
		}
		if f.Pages != nil {
			it.Pages = *f.Pages
		}
		if p, ok := pos[f.ID]; ok {
			it.ShelfID = p[0].(string)
			it.Index = p[1].(int)
		}
		items = append(items, it)
		return true
	})
	sort.Slice(items, func(i, j int) bool {
		if items[i].Series != items[j].Series {
			return items[i].Series < items[j].Series
		}
		ni, oki := domain.NumberValue(items[i].Number)
		nj, okj := domain.NumberValue(items[j].Number)
		if oki && okj && ni != nj {
			return ni < nj
		}
		return items[i].Rel < items[j].Rel
	})
	return items
}

// SpineColors 计算并缓存每本书的封面主色（书脊视图用，懒触发）。
func (a *App) SpineColors() map[string]string {
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, f := range a.facts() {
		if _, ok := a.colors[f.ID]; ok {
			continue
		}
		c := "#4a4f59"
		if f.Art.Cover {
			if p, ok := parse.ForPath(f.Rel); ok {
				if res, err := p.Parse(a.abs(f.Rel)); err == nil && len(res.Cover) > 0 {
					if col, err := thumb.AverageColor(res.Cover); err == nil {
						c = fmt.Sprintf("#%02x%02x%02x", col.R, col.G, col.B)
					}
				}
			}
		}
		a.colors[f.ID] = c
	}
	out := make(map[string]string, len(a.colors))
	for k, v := range a.colors {
		out[k] = v
	}
	return out
}

func (a *App) facts() []store.Fact {
	var out []store.Fact
	a.idx.Each(func(f store.Fact) bool { out = append(out, f); return true })
	return out
}

func (a *App) abs(rel string) string { return filepath.Join(a.root, filepath.FromSlash(rel)) }

// ---------- 摆放与覆盖（意图账） ----------

func (a *App) Shelves() []store.ShelfDef {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.shelf.Shelves == nil {
		return []store.ShelfDef{}
	}
	return a.shelf.Shelves
}

func (a *App) CreateShelf(name string) (store.ShelfDef, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	id := "s_" + domain.UnstableID(name, time.Now().UnixNano(), 0)[:6]
	sh := store.ShelfDef{ID: id, Name: name, Order: []string{}}
	a.shelf.Shelves = append(a.shelf.Shelves, sh)
	return sh, a.saveShelfLocked()
}

func (a *App) RenameShelf(id, name string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	for i := range a.shelf.Shelves {
		if a.shelf.Shelves[i].ID == id {
			a.shelf.Shelves[i].Name = name
			return a.saveShelfLocked()
		}
	}
	return fmt.Errorf("书架不存在：%s", id)
}

func (a *App) DeleteShelf(id string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := a.shelf.Shelves[:0]
	for _, s := range a.shelf.Shelves {
		if s.ID != id {
			out = append(out, s)
		}
	}
	a.shelf.Shelves = out
	return a.saveShelfLocked()
}

// MoveItem 把一本书放到某书架的指定位置（index < 0 表示追加到末尾）。
func (a *App) MoveItem(id, shelfID string, index int) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	// 先从原书架移除
	for i := range a.shelf.Shelves {
		sh := &a.shelf.Shelves[i]
		for j, x := range sh.Order {
			if x == id {
				sh.Order = append(sh.Order[:j], sh.Order[j+1:]...)
				break
			}
		}
	}
	if shelfID == "" {
		return a.saveShelfLocked() // 移出所有书架
	}
	for i := range a.shelf.Shelves {
		sh := &a.shelf.Shelves[i]
		if sh.ID != shelfID {
			continue
		}
		if index < 0 || index > len(sh.Order) {
			index = len(sh.Order)
		}
		sh.Order = append(sh.Order, "")
		copy(sh.Order[index+1:], sh.Order[index:])
		sh.Order[index] = id
		return a.saveShelfLocked()
	}
	return fmt.Errorf("书架不存在：%s", shelfID)
}

func (a *App) SetOverride(id string, ov store.Override) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.shelf.Overrides == nil {
		a.shelf.Overrides = map[string]store.Override{}
	}
	a.shelf.Overrides[id] = ov
	return a.saveShelfLocked()
}

// saveShelfLocked 写本机意图账（原子 + 备份）；按设置异步回写库内快照。
func (a *App) saveShelfLocked() error {
	dir := a.shelfDir()
	if err := a.shelf.Save(filepath.Join(dir, "shelf.json"), filepath.Join(dir, "backups")); err != nil {
		return err
	}
	a.fire("shelf:changed", map[string]int64{"rev": a.shelf.Rev})
	if a.cfg.Settings.WriteBack {
		go a.syncToLibrary()
	}
	return nil
}

// syncToLibrary 把意图快照写进库内 .bookrest/（异步、失败保留 dirty 状态）。
func (a *App) syncToLibrary() {
	a.mu.Lock()
	root, shelf, syncIndex := a.root, a.shelf, a.cfg.Settings.SyncIndex
	idx := a.idx
	a.mu.Unlock()
	if root == "" {
		return
	}
	if _, err := os.Stat(root); err != nil {
		a.fire("sync:state", map[string]any{"ok": false, "error": "库离线，快照未回写"})
		return
	}
	dir := filepath.Join(root, ".bookrest")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		a.fire("sync:state", map[string]any{"ok": false, "error": err.Error()})
		return
	}
	// 冲突检测：库内 rev 更高说明别的设备写过 → 备份 + 提示，不静默覆盖
	remotePath := filepath.Join(dir, "shelf.json")
	if rs, _, err := store.LoadShelf(remotePath, filepath.Join(dir, "backups")); err == nil && rs.Rev > shelf.Rev {
		bk := filepath.Join(dir, "shelf.conflict-"+time.Now().Format("20060102-150405")+".json")
		if data, rerr := os.ReadFile(remotePath); rerr == nil {
			_ = store.WriteFileAtomic(bk, data)
		}
		a.fire("sync:conflict", map[string]any{"backup": bk, "localRev": shelf.Rev, "remoteRev": rs.Rev})
		return
	}
	data, err := json.MarshalIndent(shelf, "", "  ")
	if err != nil {
		return
	}
	if err := store.WriteFileAtomic(remotePath, data); err != nil {
		a.fire("sync:state", map[string]any{"ok": false, "error": err.Error()})
		return
	}
	if syncIndex {
		if data, err := idx.MarshalJSONL(); err == nil {
			_ = store.WriteFileAtomic(filepath.Join(dir, "index.jsonl"), data)
		}
	}
	a.fire("sync:state", map[string]any{"ok": true, "rev": shelf.Rev, "path": dir})
}

// ---------- 体检 / 设置 / 系统集成 ----------

func (a *App) Report() *report.Report {
	a.mu.Lock()
	defer a.mu.Unlock()
	return report.Run(a.root, a.idx, a.shelf.Overrides)
}

func (a *App) Settings() config.Settings {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.cfg.Settings
}

func (a *App) SaveSettings(s config.Settings) error {
	defer metadata.Configure(s.Proxy) // 保存后立刻生效，无需重启
	a.mu.Lock()
	defer a.mu.Unlock()
	a.cfg.Settings = s
	return a.cfg.Save(a.cfgFn)
}

func (a *App) OpenFile(id string) (string, error) {
	a.mu.Lock()
	f, ok := a.idx.Get(id)
	root := a.root
	openWith := a.cfg.Settings.OpenWithMap
	a.mu.Unlock()
	if !ok {
		return "", fmt.Errorf("未知条目：%s", id)
	}
	abs := filepath.Join(root, filepath.FromSlash(f.Rel))
	return abs, openPath(abs, openWith[strings.ToLower(f.Ext)])
}

func (a *App) RevealFile(id string) (string, error) {
	a.mu.Lock()
	f, ok := a.idx.Get(id)
	root := a.root
	a.mu.Unlock()
	if !ok {
		return "", fmt.Errorf("未知条目：%s", id)
	}
	abs := filepath.Join(root, filepath.FromSlash(f.Rel))
	return abs, revealPath(abs)
}

func (a *App) CopyPath(id string) (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	f, ok := a.idx.Get(id)
	if !ok {
		return "", fmt.Errorf("未知条目：%s", id)
	}
	return filepath.Join(a.root, filepath.FromSlash(f.Rel)), nil
}

// ThumbBytes 返回某书的封面缩略图（生成后缓存，供 Wails 资源处理器使用）。
func (a *App) ThumbBytes(id string) ([]byte, error) {
	a.mu.Lock()
	if data, ok := a.thumbs[id]; ok {
		a.mu.Unlock()
		return data, nil
	}
	a.mu.Unlock()
	// 用户指定封面优先，其次文件内嵌封面
	raw, err := a.rawCover(id)
	if err != nil {
		return nil, err
	}
	data, err := thumb.CoverJPEG(raw)
	if err != nil {
		return nil, err
	}
	a.mu.Lock()
	a.thumbs[id] = data
	a.mu.Unlock()
	return data, nil
}

// SetEmitter 由外壳注入事件通道（Wails 的 runtime.EventsEmit / 测试用回调）。
func (a *App) SetEmitter(emit Emit) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.emit = emit
}

func (a *App) fire(event string, data any) {
	if a.emit != nil {
		a.emit(event, data)
	}
}

func samePath(a, b string) bool { return filepath.Clean(a) == filepath.Clean(b) }
