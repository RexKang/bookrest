package app

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/RexKang/bookrest/internal/testutil"
)

func findItem(t *testing.T, a *App, id string) Item {
	t.Helper()
	for _, it := range a.Items() {
		if it.ID == id {
			return it
		}
	}
	t.Fatalf("未找到条目 %s", id)
	return Item{}
}

func tinyPNG(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for x := 0; x < w; x++ {
		for y := 0; y < h; y++ {
			img.Set(x, y, color.RGBA{uint8(x * 7), uint8(y * 5), 120, 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// TestCoverOverrideFlow 覆盖「手动粘贴封面」全流程：设置 → 缩略图/大图生效 → 清除。
func TestCoverOverrideFlow(t *testing.T) {
	a, _, _ := setup(t)
	if _, err := a.Scan(); err != nil {
		t.Fatal(err)
	}
	id := a.Items()[0].ID

	pngBytes := tinyPNG(t, 40, 60)
	b64 := base64.StdEncoding.EncodeToString(pngBytes)
	if err := a.SetCoverFromData(id, "data:image/png;base64,"+b64, ""); err != nil {
		t.Fatal(err)
	}
	it := findItem(t, a, id)
	if !it.HasCover || it.CoverSource != "手动粘贴" {
		t.Fatalf("封面标记未生效：%+v", it)
	}
	// 缓存目录里应有一张 png
	entries, err := os.ReadDir(a.coverDir())
	if err != nil || len(entries) == 0 {
		t.Fatalf("封面缓存未落盘：%v", err)
	}
	// 缩略图与大图都应是 JPEG（由用户封面派生）
	for name, fn := range map[string]func(string) ([]byte, error){"thumb": a.ThumbBytes, "cover": a.CoverBytes} {
		data, err := fn(id)
		if err != nil {
			t.Fatalf("%s 失败：%v", name, err)
		}
		if len(data) < 3 || data[0] != 0xFF || data[1] != 0xD8 {
			t.Fatalf("%s 不是 JPEG", name)
		}
	}
	// 非法输入应被拒绝，且不破坏已有封面
	if err := a.SetCoverFromData(id, "data:image/png;base64,bm90LWFuLWltYWdl", ""); err == nil {
		t.Fatal("非图片数据应报错")
	}
	if !findItem(t, a, id).HasCover {
		t.Fatal("失败操作不应清掉已有封面")
	}
	// 清除后回退到内嵌封面
	if err := a.ClearCover(id); err != nil {
		t.Fatal(err)
	}
	if findItem(t, a, id).HasCover {
		t.Fatal("清除后不应还标记为用户封面")
	}
	entries, _ = os.ReadDir(a.coverDir())
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".png" {
			t.Fatalf("清除后缓存文件应删除，仍有 %s", e.Name())
		}
	}
	// 未知条目要报错
	if err := a.SetCoverFromData("no-such-id", "data:image/png;base64,"+b64, ""); err == nil {
		t.Fatal("未知条目应报错")
	}
}

// TestReportNoCoverRule：「无封面」只统计本该能提取封面的格式，且尊重用户手动封面。
func TestReportNoCoverRule(t *testing.T) {
	a, lib, _ := setup(t)
	// 无封面的 cbz（应计入）+ 一个 pdf（不该计入）
	if err := testutil.WriteCBZ(filepath.Join(lib, "SeriesB", "无封面 第9卷.cbz"),
		testutil.CBZOpts{NoCover: true, ComicInfo: true, Series: "SeriesB", Number: "9"}); err != nil {
		t.Fatal(err)
	}
	pdfPath := filepath.Join(lib, "Docs", "报告 2024.pdf")
	if err := os.MkdirAll(filepath.Dir(pdfPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(pdfPath, []byte("%PDF-1.4 fake"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Scan(); err != nil {
		t.Fatal(err)
	}

	noCover := func() []string {
		var out []string
		for _, f := range a.Report().Findings {
			if f.Kind == "nocover" {
				out = append(out, f.Items...)
			}
		}
		return out
	}
	list := noCover()
	var hasCBZ, hasPDF bool
	for _, rel := range list {
		if filepath.Base(rel) == "无封面 第9卷.cbz" {
			hasCBZ = true
		}
		if filepath.Base(rel) == "报告 2024.pdf" {
			hasPDF = true
		}
	}
	if !hasCBZ {
		t.Fatalf("无封面的 CBZ 应计入体检，得到 %v", list)
	}
	if hasPDF {
		t.Fatalf("PDF 不支持自动提取封面，不应计入「无封面」，得到 %v", list)
	}
	// 给这个 cbz 手动配一张封面 → 应从报告里消失
	var target string
	for _, it := range a.Items() {
		if filepath.Base(it.Rel) == "无封面 第9卷.cbz" {
			target = it.ID
		}
	}
	if target == "" {
		t.Fatal("未找到目标条目")
	}
	if err := a.SetCoverFromData(target, "data:image/png;base64,"+base64.StdEncoding.EncodeToString(tinyPNG(t, 24, 36)), ""); err != nil {
		t.Fatal(err)
	}
	for _, rel := range noCover() {
		if filepath.Base(rel) == "无封面 第9卷.cbz" {
			t.Fatalf("已手动配封面的条目不应再计入「无封面」")
		}
	}
}
