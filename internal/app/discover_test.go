package app

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/RexKang/bookrest/internal/testutil"
)

// TestDiscoverFindsCandidateDirs 覆盖全盘找书：命中聚合、阈值过滤、常见目录屏蔽。
func TestDiscoverFindsCandidateDirs(t *testing.T) {
	base := t.TempDir()
	must := func(err error) {
		if err != nil {
			t.Fatal(err)
		}
	}
	// 一片漫画区：6 本 cbz
	for i := 1; i <= 6; i++ {
		must(testutil.WriteCBZ(filepath.Join(base, "Comics", "SeriesA", fmt.Sprintf("v%d.cbz", i)),
			testutil.CBZOpts{Pages: 4, ComicInfo: true, Series: "SeriesA", Number: fmt.Sprint(i), Seed: int64(i)}))
	}
	// 一片电子书区：只有 2 本 pdf（低于阈值）
	must(os.MkdirAll(filepath.Join(base, "Docs"), 0o755))
	for _, n := range []string{"a.pdf", "b.pdf"} {
		must(os.WriteFile(filepath.Join(base, "Docs", n), []byte("%PDF-1.4 fake"), 0o644))
	}
	// 系统目录：3 本 epub，应被「屏蔽常见目录」挡掉
	must(os.MkdirAll(filepath.Join(base, "Windows", "System32"), 0o755))
	for _, n := range []string{"x.epub", "y.epub", "z.epub"} {
		must(os.WriteFile(filepath.Join(base, "Windows", "System32", n), []byte("epub"), 0o644))
	}
	// 非书籍文件不应计入
	must(os.WriteFile(filepath.Join(base, "Comics", "SeriesA", "note.txt"), []byte("x"), 0o644))

	a, _, _ := setup(t)
	res, err := a.Discover(DiscoverOptions{
		Drives:        []string{base},
		Extensions:    []string{".cbz", ".pdf", ".epub"},
		ExcludeCommon: true,
		MinBooks:      5,
		MaxDepth:      6,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Hits) != 1 {
		t.Fatalf("应只命中 1 个候选库目录，得到 %+v", res.Hits)
	}
	h := res.Hits[0]
	if h.Books != 6 || filepath.Base(h.Dir) != "SeriesA" {
		t.Fatalf("候选库目录异常：%+v", h)
	}
	if res.ScannedFiles != 8 { // 6 本 cbz + 2 本 pdf；Windows 下的 3 本 epub 被屏蔽
		t.Fatalf("命中文件数应为 8，得到 %d", res.ScannedFiles)
	}
	// 关掉屏蔽后，Windows 下的 3 本也会被看到
	res2, err := a.Discover(DiscoverOptions{
		Drives:        []string{base},
		Extensions:    []string{".epub"},
		ExcludeCommon: false,
		MinBooks:      3,
		MaxDepth:      6,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(res2.Hits) != 1 || res2.Hits[0].Books != 3 {
		t.Fatalf("关闭屏蔽后应看到 System32 下的 3 本，得到 %+v", res2.Hits)
	}
	// 额外排除
	res3, err := a.Discover(DiscoverOptions{
		Drives:        []string{base},
		Extensions:    []string{".cbz"},
		ExcludeCommon: false,
		ExtraExcludes: []string{"SeriesA"},
		MinBooks:      1,
		MaxDepth:      6,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(res3.Hits) != 0 {
		t.Fatalf("额外排除应生效，得到 %+v", res3.Hits)
	}
	// 深度上限：深度 1 时应看不到 Comics/SeriesA
	res4, err := a.Discover(DiscoverOptions{
		Drives:        []string{base},
		Extensions:    []string{".cbz"},
		ExcludeCommon: false,
		MinBooks:      1,
		MaxDepth:      1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(res4.Hits) != 0 {
		t.Fatalf("深度上限应生效，得到 %+v", res4.Hits)
	}
}

// TestDiscoverDoesNotWrite 是全盘找书的红线：只读，绝不写盘。
func TestDiscoverDoesNotWrite(t *testing.T) {
	base := t.TempDir()
	must := func(err error) {
		if err != nil {
			t.Fatal(err)
		}
	}
	dir := filepath.Join(base, "Lib")
	for i := 1; i <= 5; i++ {
		must(testutil.WriteCBZ(filepath.Join(dir, fmt.Sprintf("b%d.cbz", i)), testutil.CBZOpts{Pages: 3, Seed: int64(i)}))
	}
	before := snapshotTree(t, base)

	a, _, _ := setup(t)
	if _, err := a.Discover(DiscoverOptions{Drives: []string{base}, MinBooks: 3}); err != nil {
		t.Fatal(err)
	}
	after := snapshotTree(t, base)
	if len(before) != len(after) {
		t.Fatalf("文件数变化：%d → %d", len(before), len(after))
	}
	for k, v := range before {
		if after[k] != v {
			t.Fatalf("文件被改动：%s（%s → %s）", k, v, after[k])
		}
	}
}

// snapshotTree 记录整棵树的 相对路径 → 大小+mtime（用于证明零写入）。
func snapshotTree(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		fi, e := d.Info()
		if e != nil {
			return e
		}
		rel, _ := filepath.Rel(root, path)
		out[rel] = fmt.Sprintf("%d|%d", fi.Size(), fi.ModTime().UnixNano())
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}
