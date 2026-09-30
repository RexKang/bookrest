package scan

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/RexKang/bookrest/internal/store"
	"github.com/RexKang/bookrest/internal/testutil"
)

type env struct {
	root  string // 库根
	meta  string // 镜像目录（索引/状态）
	idx   *store.Index
	state *store.ScanState
}

// dirMtime 返回目录 mtime（纳秒）。
func dirMtime(dir string) (int64, error) {
	fi, err := os.Stat(dir)
	if err != nil {
		return 0, err
	}
	return fi.ModTime().UnixNano(), nil
}

// waitDirMtimeChange 轮询等待目录 mtime 落定（NTFS 上目录时间戳是延迟补写的）。
// 重命名不改变条目数，只能依赖 mtime 判据，因此测试里等它稳定后再扫描。
func waitDirMtimeChange(t *testing.T, dir string, before int64) {
	t.Helper()
	for i := 0; i < 300; i++ {
		if fi, err := os.Stat(dir); err == nil && fi.ModTime().UnixNano() != before {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("目录 mtime 在 3s 内未更新: %s", dir)
}

func newEnv(t *testing.T) *env {
	t.Helper()
	base := t.TempDir()
	root := filepath.Join(base, "lib")
	meta := filepath.Join(base, "meta")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(meta, 0o755); err != nil {
		t.Fatal(err)
	}
	return &env{
		root:  root,
		meta:  meta,
		idx:   store.NewIndex(filepath.Join(meta, "index.jsonl")),
		state: store.NewScanState(root),
	}
}

func (e *env) scan(t *testing.T) (Stats, Diff) {
	t.Helper()
	sc := &Scanner{Root: e.root}
	diff, err := sc.ScanIncremental(e.idx, e.state)
	if err != nil {
		t.Fatalf("扫描失败: %v", err)
	}
	if err := e.idx.Save(); err != nil {
		t.Fatal(err)
	}
	if err := e.state.Save(filepath.Join(e.meta, "scan_state.json")); err != nil {
		t.Fatal(err)
	}
	return sc.Stats, diff
}

func (e *env) reload(t *testing.T) {
	t.Helper()
	idx := store.NewIndex(filepath.Join(e.meta, "index.jsonl"))
	if err := idx.Load(); err != nil {
		t.Fatalf("索引重载失败: %v", err)
	}
	st, err := store.LoadScanState(filepath.Join(e.meta, "scan_state.json"), e.root)
	if err != nil {
		t.Fatalf("状态重载失败: %v", err)
	}
	e.idx, e.state = idx, st
}

func buildLibrary(t *testing.T, root string) {
	t.Helper()
	must := func(err error) {
		if err != nil {
			t.Fatal(err)
		}
	}
	must(testutil.WriteCBZ(filepath.Join(root, "SeriesA", "v1.cbz"),
		testutil.CBZOpts{Pages: 5, ComicInfo: true, Series: "SeriesA", Number: "1", Seed: 11}))
	must(testutil.WriteCBZ(filepath.Join(root, "SeriesA", "v2.cbz"),
		testutil.CBZOpts{Pages: 6, ComicInfo: true, Series: "SeriesA", Number: "2", Seed: 12}))
	must(testutil.WriteCBZ(filepath.Join(root, "SeriesB", "v3.cbz"),
		testutil.CBZOpts{Pages: 7, ComicInfo: true, Series: "SeriesB", Number: "3", Seed: 13}))
	must(testutil.WriteEPUB(filepath.Join(root, "SeriesB", "book.epub"),
		testutil.EPUBOpts{Title: "书", Author: "作者", WithMeta: true, Pages: 2, Seed: 14}))
}

// 全生命周期：首扫 → 无变化重扫（剪枝）→ 新增 → 重命名 → 删除。
func TestScanLifecycle(t *testing.T) {
	e := newEnv(t)
	buildLibrary(t, e.root)

	// 阶段 1：首次全量
	st1, diff1 := e.scan(t)
	if st1.Added != 4 || len(diff1.Added) != 4 {
		t.Fatalf("首扫应新增 4 本，得到 Added=%d diff=%v", st1.Added, diff1.Added)
	}
	if st1.Parsed != 4 {
		t.Fatalf("应解析 4 本，得到 %d", st1.Parsed)
	}
	if e.idx.Len() != 4 {
		t.Fatalf("索引应有 4 条，得到 %d", e.idx.Len())
	}

	v1, ok := e.idx.ByRel("SeriesA/v1.cbz")
	if !ok {
		t.Fatal("SeriesA/v1.cbz 应入索引")
	}
	if v1.Pages == nil || *v1.Pages != 5 {
		t.Fatalf("页数应为 5，得到 %v", v1.Pages)
	}
	if v1.Meta.Series != "SeriesA" || v1.Meta.Number != "1" {
		t.Fatalf("元数据异常: %+v", v1.Meta)
	}

	// 阶段 2：无变化重扫——目录时间戳可能刚被系统补写，本轮只允许 stat，不允许重哈希/解析
	e.reload(t)
	st2, diff2 := e.scan(t)
	if st2.FilesHashed != 0 || st2.Parsed != 0 {
		t.Fatalf("无变化重扫不应重新哈希/解析: hash=%d parse=%d", st2.FilesHashed, st2.Parsed)
	}
	if len(diff2.Added) != 0 {
		t.Fatalf("不应有新增: %v", diff2.Added)
	}

	// 阶段 2b：库已静止——此时应整棵剪枝，连 stat 都不做
	e.reload(t)
	st2b, _ := e.scan(t)
	if st2b.FilesStatted != 0 {
		t.Fatalf("静止库重扫不应 stat 文件，得到 %d", st2b.FilesStatted)
	}
	if st2b.PrunedDirs < 2 {
		t.Fatalf("应剪枝至少 2 个目录（SeriesA/SeriesB），得到 %d", st2b.PrunedDirs)
	}
	if st2b.Missing != 0 {
		t.Fatalf("剪枝不应把未访问的条目误判为缺失，得到 %d", st2b.Missing)
	}

	// 阶段 3：新增一本——只有变化目录内的文件被 stat
	if err := testutil.WriteCBZ(filepath.Join(e.root, "SeriesA", "v4.cbz"),
		testutil.CBZOpts{Pages: 4, ComicInfo: true, Series: "SeriesA", Number: "4", Seed: 15}); err != nil {
		t.Fatal(err)
	}
	e.reload(t)
	st3, diff3 := e.scan(t)
	if st3.Added != 1 || len(diff3.Added) != 1 {
		t.Fatalf("应新增 1 本，得到 Added=%d", st3.Added)
	}
	if st3.FilesStatted != 3 {
		t.Fatalf("应 stat SeriesA 内 3 个文件，得到 %d", st3.FilesStatted)
	}
	if st3.FilesHashed != 1 {
		t.Fatalf("应只哈希 1 个新文件，得到 %d", st3.FilesHashed)
	}
	if st3.PrunedDirs != 1 {
		t.Fatalf("SeriesB 应被剪枝（1 个目录），得到 %d", st3.PrunedDirs)
	}

	// 阶段 4：重命名——身份不变，摆放可跟随
	dirA := filepath.Join(e.root, "SeriesA")
	beforeMtime, err := dirMtime(dirA)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(dirA, "v1.cbz"), filepath.Join(dirA, "v1-renamed.cbz")); err != nil {
		t.Fatal(err)
	}
	waitDirMtimeChange(t, dirA, beforeMtime)
	e.reload(t)
	st4, diff4 := e.scan(t)
	if st4.Moved != 1 || len(diff4.Moved) != 1 {
		t.Fatalf("应识别 1 次移动，得到 Moved=%d", st4.Moved)
	}
	if st4.Added != 0 {
		t.Fatalf("重命名不应算新增，得到 %d", st4.Added)
	}
	moved, ok := e.idx.ByRel("SeriesA/v1-renamed.cbz")
	if !ok {
		t.Fatal("重命名后的路径应入索引")
	}
	if moved.ID != v1.ID {
		t.Fatalf("重命名后身份应保持不变: %s → %s", v1.ID, moved.ID)
	}

	// 阶段 5：删除——标记 missing，不立即删除记录
	if err := os.Remove(filepath.Join(e.root, "SeriesA", "v2.cbz")); err != nil {
		t.Fatal(err)
	}
	e.reload(t)
	st5, diff5 := e.scan(t)
	if st5.Missing != 1 || len(diff5.Missing) != 1 {
		t.Fatalf("应标记 1 条缺失，得到 %d", st5.Missing)
	}
	v2id := ""
	e.idx.Each(func(f store.Fact) bool {
		if f.Rel == "SeriesA/v2.cbz" {
			v2id = f.ID
		}
		return true
	})
	if v2id == "" {
		t.Fatal("被删除的文件记录应保留（标记 missing）")
	}
	if f, _ := e.idx.Get(v2id); !f.Flags.Missing {
		t.Fatal("缺失标记未写入")
	}
}

// 源文件零写入：整轮扫描前后文件 size/mtime/hash 不变。
func TestScanDoesNotTouchSourceFiles(t *testing.T) {
	e := newEnv(t)
	buildLibrary(t, e.root)

	target := filepath.Join(e.root, "SeriesA", "v1.cbz")
	s1, m1, h1, err := testutil.FileDigest(target)
	if err != nil {
		t.Fatal(err)
	}
	e.scan(t)
	e.reload(t)
	e.scan(t)
	s2, m2, h2, err := testutil.FileDigest(target)
	if err != nil {
		t.Fatal(err)
	}
	if s1 != s2 || m1 != m2 || h1 != h2 {
		t.Fatalf("扫描改动了源文件: %d/%d/%s → %d/%d/%s", s1, m1, h1, s2, m2, h2)
	}
}

// 不支持的扩展名只索引不解析。
func TestUnsupportedExtensionIndexedOnly(t *testing.T) {
	e := newEnv(t)
	if err := os.MkdirAll(filepath.Join(e.root, "Misc"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(e.root, "Misc", "某文档 第2卷.pdf"), []byte("%PDF-1.4 fake"), 0o644); err != nil {
		t.Fatal(err)
	}
	st, _ := e.scan(t)
	if st.Parsed != 0 {
		t.Fatalf("PDF 不应被解析，得到 Parsed=%d", st.Parsed)
	}
	f, ok := e.idx.ByRel("Misc/某文档 第2卷.pdf")
	if !ok {
		t.Fatal("应仍然入索引")
	}
	if f.Meta.Number != "2" {
		t.Fatalf("应走文件名解析得到卷号 2，得到 %q", f.Meta.Number)
	}
}
