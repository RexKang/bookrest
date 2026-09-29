package parse

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/RexKang/bookrest/internal/domain"
	"github.com/RexKang/bookrest/internal/testutil"
)

// TC-P-01：CBZ + ComicInfo.xml，元数据来自内嵌。
func TestCBZWithComicInfo(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "vol1.cbz")
	if err := testutil.WriteCBZ(p, testutil.CBZOpts{Pages: 6, ComicInfo: true, Series: "测试系列", Number: "1", Title: "第一卷", Seed: 1}); err != nil {
		t.Fatal(err)
	}
	pr, ok := ForPath(p)
	if !ok {
		t.Fatal("应匹配到 CBZ 解析器")
	}
	res, err := pr.Parse(p)
	if err != nil {
		t.Fatal(err)
	}
	if res.Meta.Series != "测试系列" || res.Meta.Number != "1" || res.Meta.Author != "测试作者" {
		t.Fatalf("内嵌元数据未读出: %+v", res.Meta)
	}
	if res.Meta.Src != domain.SrcComicInfo {
		t.Fatalf("Src 应为 comicinfo，得到 %q", res.Meta.Src)
	}
	if res.Pages != 6 {
		t.Fatalf("页数应为 6，得到 %d", res.Pages)
	}
	if len(res.Cover) == 0 {
		t.Fatal("应取到封面字节")
	}
	if !res.Embedded {
		t.Fatal("Embedded 应为 true")
	}
}

// TC-P-02：无 ComicInfo → 无内嵌元数据，但页数与封面仍可用。
func TestCBZWithoutComicInfo(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "vol2.cbz")
	if err := testutil.WriteCBZ(p, testutil.CBZOpts{Pages: 4, Seed: 2}); err != nil {
		t.Fatal(err)
	}
	pr, _ := ForPath(p)
	res, err := pr.Parse(p)
	if err != nil {
		t.Fatal(err)
	}
	if res.Embedded {
		t.Fatal("无 ComicInfo 时 Embedded 应为 false")
	}
	if res.Pages != 4 || len(res.Cover) == 0 {
		t.Fatalf("页数/封面异常: pages=%d cover=%d", res.Pages, len(res.Cover))
	}
}

// TC-P-05：页数极值。
func TestCBZPageCounts(t *testing.T) {
	dir := t.TempDir()
	for _, n := range []int{1, 12} {
		p := filepath.Join(dir, "p.cbz")
		if err := testutil.WriteCBZ(p, testutil.CBZOpts{Pages: n, Seed: int64(n)}); err != nil {
			t.Fatal(err)
		}
		pr, _ := ForPath(p)
		res, err := pr.Parse(p)
		if err != nil {
			t.Fatal(err)
		}
		if res.Pages != n {
			t.Fatalf("页数应为 %d，得到 %d", n, res.Pages)
		}
	}
}

// TC-P-04：损坏族——截断 zip、0 字节、伪装扩展名，都必须返回错误而不是 panic。
func TestCorruptArchives(t *testing.T) {
	dir := t.TempDir()

	truncated := filepath.Join(dir, "truncated.cbz")
	if err := testutil.WriteCBZ(truncated, testutil.CBZOpts{Pages: 6, Corrupt: true}); err != nil {
		t.Fatal(err)
	}
	zero := filepath.Join(dir, "zero.cbz")
	if err := os.WriteFile(zero, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	fake := filepath.Join(dir, "fake.cbz")
	if err := os.WriteFile(fake, []byte("这不是一个 zip 文件，只是改了扩展名"), 0o644); err != nil {
		t.Fatal(err)
	}

	pr, _ := For(".cbz")
	for _, p := range []string{truncated, zero, fake} {
		if _, err := pr.Parse(p); err == nil {
			t.Errorf("%s 应返回错误", filepath.Base(p))
		}
	}
}

// TC-P-06：解析全程只读，源文件 size/mtime/内容哈希不变。
func TestParseIsReadOnly(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "ro.cbz")
	if err := testutil.WriteCBZ(p, testutil.CBZOpts{Pages: 3, ComicInfo: true, Series: "S", Number: "1", Seed: 9}); err != nil {
		t.Fatal(err)
	}
	size1, mtime1, hash1, err := testutil.FileDigest(p)
	if err != nil {
		t.Fatal(err)
	}
	pr, _ := ForPath(p)
	if _, err := pr.Parse(p); err != nil {
		t.Fatal(err)
	}
	size2, mtime2, hash2, err := testutil.FileDigest(p)
	if err != nil {
		t.Fatal(err)
	}
	if size1 != size2 || mtime1 != mtime2 || hash1 != hash2 {
		t.Fatalf("解析修改了源文件: %d/%d/%s → %d/%d/%s", size1, mtime1, hash1, size2, mtime2, hash2)
	}
}

// TC-P-03：EPUB + OPF，标题作者来自 OPF，封面可用。
func TestEPUBWithOPF(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "book.epub")
	if err := testutil.WriteEPUB(p, testutil.EPUBOpts{Title: "测试电子书", Author: "作者甲", WithMeta: true, Pages: 3, Seed: 5}); err != nil {
		t.Fatal(err)
	}
	pr, ok := ForPath(p)
	if !ok {
		t.Fatal("应匹配到 EPUB 解析器")
	}
	res, err := pr.Parse(p)
	if err != nil {
		t.Fatal(err)
	}
	if res.Meta.Title != "测试电子书" || res.Meta.Author != "作者甲" {
		t.Fatalf("OPF 元数据未读出: %+v", res.Meta)
	}
	if res.Meta.Src != domain.SrcOPF {
		t.Fatalf("Src 应为 opf，得到 %q", res.Meta.Src)
	}
	if len(res.Cover) == 0 {
		t.Fatal("应取到 EPUB 封面")
	}
	if res.Pages != 3 {
		t.Fatalf("章节数应为 3，得到 %d", res.Pages)
	}
}
