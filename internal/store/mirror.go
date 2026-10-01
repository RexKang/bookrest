package store

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/RexKang/bookrest/internal/domain"
)

// ---------- 事实账（可重建） ----------

// Fact 是 index.jsonl 的一行（详细设计 §3.1）。
type Fact struct {
	V     int                `json:"v"`
	ID    string             `json:"id"`
	FP    domain.Fingerprint `json:"fp"`
	Rel   string             `json:"rel"`
	Ext   string             `json:"ext"`
	Pages *int               `json:"pages,omitempty"`
	Meta  domain.Meta        `json:"meta"`
	Art   Art                `json:"art"`
	Seen  Seen               `json:"seen"`
	Flags Flags              `json:"flags"`
	// Dups 记录与本体字节完全相同的其他路径（精确重复）。
	// 身份以字节为准，所以重复文件不会各占一条记录，而是挂在同一身份下。
	Dups []string `json:"dups,omitempty"`
}

type Art struct {
	Cover  bool `json:"cover"`
	Thumb  bool `json:"thumb"`
	Spine  bool `json:"spine"`
	Failed bool `json:"failed"`
}

type Seen struct {
	First int64 `json:"first"`
	Last  int64 `json:"last"`
}

type Flags struct {
	Missing    bool   `json:"missing,omitempty"`
	IDUnstable bool   `json:"id_unstable,omitempty"`
	DupOf      string `json:"dup_of,omitempty"`
}

// Index 是内存中的事实账，落盘为 JSONL。
type Index struct {
	path  string
	items map[string]Fact // id → fact
	byRel map[string]string
}

func NewIndex(path string) *Index {
	return &Index{path: path, items: map[string]Fact{}, byRel: map[string]string{}}
}

func (ix *Index) Path() string { return ix.path }

// Load 逐行解析；坏行跳过。坏行超过 20% 时返回错误（调用方应丢弃重建）。
func (ix *Index) Load() error {
	f, err := os.Open(ix.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 1<<20), 1<<20)
	total, bad := 0, 0
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		total++
		var fact Fact
		if err := json.Unmarshal(line, &fact); err != nil || fact.ID == "" {
			bad++
			continue
		}
		ix.items[fact.ID] = fact
		ix.byRel[fact.Rel] = fact.ID
	}
	if err := sc.Err(); err != nil {
		return err
	}
	// 容忍个别坏行（崩溃残留），但大面积损坏说明文件不可信 → 交给上层丢弃重建
	if bad > 1 && bad*5 > total {
		return fmt.Errorf("index: %d/%d 行损坏，建议重建", bad, total)
	}
	return nil
}

// MarshalJSONL 把事实账序列化为 JSONL（按路径排序，便于 diff）。
func (ix *Index) MarshalJSONL() ([]byte, error) {
	list := make([]Fact, 0, len(ix.items))
	for _, f := range ix.items {
		list = append(list, f)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].Rel < list[j].Rel })
	var buf []byte
	for _, f := range list {
		line, err := json.Marshal(f)
		if err != nil {
			return nil, err
		}
		buf = append(buf, line...)
		buf = append(buf, '\n')
	}
	return buf, nil
}

func (ix *Index) Save() error {
	buf, err := ix.MarshalJSONL()
	if err != nil {
		return err
	}
	return WriteFileAtomic(ix.path, buf)
}

func (ix *Index) Get(id string) (Fact, bool) { f, ok := ix.items[id]; return f, ok }
func (ix *Index) ByRel(rel string) (Fact, bool) {
	id, ok := ix.byRel[rel]
	if !ok {
		return Fact{}, false
	}
	f, ok := ix.items[id]
	return f, ok
}
func (ix *Index) Len() int { return len(ix.items) }

func (ix *Index) Put(f Fact) {
	if old, ok := ix.items[f.ID]; ok && old.Rel != f.Rel {
		delete(ix.byRel, old.Rel)
	}
	if id, ok := ix.byRel[f.Rel]; ok && id != f.ID {
		delete(ix.items, id)
	}
	ix.items[f.ID] = f
	ix.byRel[f.Rel] = f.ID
}

func (ix *Index) Delete(id string) {
	if f, ok := ix.items[id]; ok {
		delete(ix.byRel, f.Rel)
		delete(ix.items, id)
	}
}

func (ix *Index) Each(fn func(Fact) bool) {
	list := make([]Fact, 0, len(ix.items))
	for _, f := range ix.items {
		list = append(list, f)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].Rel < list[j].Rel })
	for _, f := range list {
		if !fn(f) {
			return
		}
	}
}

// ---------- 意图账（唯一不可重建） ----------

type ShelfDef struct {
	ID    string   `json:"id"`
	Name  string   `json:"name"`
	Order []string `json:"order,omitempty"`
	Smart string   `json:"smart,omitempty"` // 规则字符串，如 "tag:画集"
}

type Override struct {
	Title   string   `json:"title,omitempty"`
	Series  string   `json:"series,omitempty"`
	Number  string   `json:"number,omitempty"`
	Author  string   `json:"author,omitempty"`
	Tags    []string `json:"tags,omitempty"`
	Rating  int      `json:"rating,omitempty"`
	Deleted bool     `json:"deleted,omitempty"`
	// Cover 是用户指定的封面（缓存文件 + 来源）。为空则用文件内嵌封面。
	Cover *CoverRef `json:"cover,omitempty"`
}

// CoverRef 指向本机缓存里的一张封面（派生数据，可删）。
type CoverRef struct {
	File   string `json:"file"`             // 镜像目录 covers/ 下的文件名
	Ext    string `json:"ext"`              // 原始扩展名（.jpg/.png/…）
	Source string `json:"source,omitempty"` // 来源：手动粘贴 / 选择文件 / 站点名
	Query  string `json:"query,omitempty"`  // 联网搜索时用的查询串（可追溯）
	At     int64  `json:"at,omitempty"`     // 设置时间（Unix 秒）
}

type UIState struct {
	ActiveShelf string `json:"active_shelf,omitempty"`
	SpineStyle  string `json:"spine_style,omitempty"`
}

type Shelf struct {
	V         int                 `json:"v"`
	Rev       int64               `json:"rev"`
	Device    string              `json:"device"`
	UpdatedAt string              `json:"updated_at"`
	Shelves   []ShelfDef          `json:"shelves"`
	Overrides map[string]Override `json:"overrides"`
	UI        UIState             `json:"ui"`
}

func NewShelf(device string) *Shelf {
	return &Shelf{V: 1, Device: device, Shelves: []ShelfDef{}, Overrides: map[string]Override{}}
}

// LoadShelf 读取意图账；文件损坏时回退到最近可用备份。
func LoadShelf(path, backupDir string) (*Shelf, string, error) {
	s, err := readShelf(path)
	if err == nil {
		return s, "", nil
	}
	if !os.IsNotExist(err) {
		if b, berr := latestBackup(backupDir, "shelf"); berr == nil && b != "" {
			if s2, err2 := readShelf(b); err2 == nil {
				return s2, b, nil
			}
		}
		return nil, "", err
	}
	return NewShelf("local"), "", nil
}

func readShelf(path string) (*Shelf, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var s Shelf
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, err
	}
	if s.Overrides == nil {
		s.Overrides = map[string]Override{}
	}
	return &s, nil
}

// Save 先备份磁盘上的旧版本，再原子写入并 rev+1。
func (s *Shelf) Save(path, backupDir string) error {
	if _, err := os.Stat(path); err == nil {
		if _, err := BackupFile(path, backupDir, "shelf", s.Rev); err != nil {
			return err
		}
	}
	s.Rev++
	s.UpdatedAt = time.Now().Format(time.RFC3339)
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	if err := WriteFileAtomic(path, data); err != nil {
		return err
	}
	_ = RotateBackups(backupDir, "shelf", 20)
	return nil
}

func latestBackup(dir, prefix string) (string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() && len(e.Name()) > len(prefix) && e.Name()[:len(prefix)] == prefix {
			names = append(names, e.Name())
		}
	}
	if len(names) == 0 {
		return "", nil
	}
	sort.Strings(names)
	return filepath.Join(dir, names[len(names)-1]), nil
}

// ---------- 扫描状态（本机，可重建） ----------

type DirInfo struct {
	MTime int64 `json:"mtime"`
	Files int   `json:"files"`
}

type ScanState struct {
	V              int                `json:"v"`
	Root           string             `json:"root"`
	Dirs           map[string]DirInfo `json:"dirs"`
	LastScan       int64              `json:"last_scan"`
	LastFullVerify int64              `json:"last_full_verify"`
}

func NewScanState(root string) *ScanState {
	return &ScanState{V: 1, Root: root, Dirs: map[string]DirInfo{}}
}

func LoadScanState(path, root string) (*ScanState, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return NewScanState(root), nil
		}
		return nil, err
	}
	var st ScanState
	if err := json.Unmarshal(data, &st); err != nil {
		return NewScanState(root), nil // 状态文件可重建，损坏直接重来
	}
	if st.Dirs == nil {
		st.Dirs = map[string]DirInfo{}
	}
	st.Root = root
	return &st, nil
}

func (st *ScanState) Save(path string) error {
	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	return WriteFileAtomic(path, data)
}

var ErrNotExist = errors.New("store: not exist")
