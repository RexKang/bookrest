package store

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/RexKang/bookrest/internal/domain"
)

func factFor(id, rel string) Fact {
	pages := 10
	return Fact{
		V: 1, ID: id, Rel: rel, Ext: ".cbz", Pages: &pages,
		FP:   domain.Fingerprint{Size: 100, Head: strings.Repeat("a", 32), MTime: 1},
		Meta: domain.Meta{Title: rel, Src: domain.SrcFilename},
	}
}

// TC-T-01：原子写——内容正确、无残留临时文件。
func TestAtomicWrite(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "sub", "shelf.json")
	if err := WriteFileAtomic(p, []byte(`{"v":1}`)); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != `{"v":1}` {
		t.Fatalf("内容不符: %s", data)
	}
	entries, err := os.ReadDir(filepath.Dir(p))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".tmp-") {
			t.Fatalf("残留临时文件: %s", e.Name())
		}
	}
}

// 意图账：rev 递增 + 滚动备份。
func TestShelfSaveRevAndBackup(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "mirror", "shelf.json")
	backups := filepath.Join(dir, "backups")

	s := NewShelf("test-device")
	s.Shelves = append(s.Shelves, ShelfDef{ID: "s_01", Name: "必读"})
	if err := s.Save(path, backups); err != nil {
		t.Fatal(err)
	}
	if s.Rev != 1 {
		t.Fatalf("首次保存后 rev 应为 1，得到 %d", s.Rev)
	}
	s.Overrides["abc"] = Override{Tags: []string{"画集"}}
	if err := s.Save(path, backups); err != nil {
		t.Fatal(err)
	}
	if s.Rev != 2 {
		t.Fatalf("第二次保存后 rev 应为 2，得到 %d", s.Rev)
	}
	entries, err := os.ReadDir(backups)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) == 0 {
		t.Fatal("应产生备份文件")
	}
}

// TC-T-02：shelf.json 损坏 → 回退最近备份。
func TestShelfCorruptRecovery(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "mirror", "shelf.json")
	backups := filepath.Join(dir, "backups")

	s := NewShelf("dev")
	s.Shelves = append(s.Shelves, ShelfDef{ID: "s_01", Name: "第一版"})
	if err := s.Save(path, backups); err != nil { // rev 1，无备份
		t.Fatal(err)
	}
	s.Shelves[0].Name = "第二版"
	if err := s.Save(path, backups); err != nil { // rev 2，备份了 rev1
		t.Fatal(err)
	}
	// 破坏当前文件
	if err := os.WriteFile(path, []byte("{ 坏掉的 json"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, from, err := LoadShelf(path, backups)
	if err != nil {
		t.Fatalf("应能回退备份，却报错: %v", err)
	}
	if from == "" {
		t.Fatal("应报告回退来源")
	}
	if len(got.Shelves) == 0 || got.Shelves[0].Name != "第一版" {
		t.Fatalf("回退内容不符: %+v", got.Shelves)
	}
}

// 事实账：JSONL 往返 + 坏行容忍 + 大面积损坏报错。
func TestIndexRoundTripAndCorruption(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "index.jsonl")
	ix := NewIndex(path)
	ix.Put(factFor("id1", "a/1.cbz"))
	ix.Put(factFor("id2", "a/2.cbz"))
	ix.Put(factFor("id3", "b/3.cbz"))
	if err := ix.Save(); err != nil {
		t.Fatal(err)
	}

	ix2 := NewIndex(path)
	if err := ix2.Load(); err != nil {
		t.Fatal(err)
	}
	if ix2.Len() != 3 {
		t.Fatalf("应载入 3 条，得到 %d", ix2.Len())
	}
	if f, ok := ix2.ByRel("a/2.cbz"); !ok || f.ID != "id2" {
		t.Fatalf("ByRel 查询失败: %+v %v", f, ok)
	}

	// 少量坏行：跳过即可
	data, _ := os.ReadFile(path)
	if err := os.WriteFile(path, append(data, []byte("{坏行}\n")...), 0o644); err != nil {
		t.Fatal(err)
	}
	ix3 := NewIndex(path)
	if err := ix3.Load(); err != nil {
		t.Fatalf("少量坏行不应报错: %v", err)
	}
	if ix3.Len() != 3 {
		t.Fatalf("坏行跳过后应仍是 3 条，得到 %d", ix3.Len())
	}

	// 大面积损坏：应报错以便上层重建
	if err := os.WriteFile(path, []byte("{坏}\n{坏}\n{坏}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := NewIndex(path).Load(); err == nil {
		t.Fatal("大面积损坏应报错")
	}
}

// 扫描状态损坏 → 直接重来，不阻塞启动。
func TestScanStateCorrupt(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "scan_state.json")
	if err := os.WriteFile(path, []byte("not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	st, err := LoadScanState(path, "D:/Books")
	if err != nil {
		t.Fatalf("状态文件损坏不应报错: %v", err)
	}
	if st.Root != "D:/Books" || len(st.Dirs) != 0 {
		t.Fatalf("应得到全新状态: %+v", st)
	}
}
