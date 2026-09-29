package thumb

import (
	"bytes"
	"fmt"
	"image"
	"image/jpeg"
	"testing"

	"github.com/RexKang/bookrest/internal/testutil"
)

// TC-M-01：封面缩略图生成——尺寸受框限制、保持比例、可解码。
func TestCoverJPEG(t *testing.T) {
	raw := testutil.MakeJPEG(800, 1200, 1)
	out, err := CoverJPEG(raw)
	if err != nil {
		t.Fatal(err)
	}
	img, err := jpeg.Decode(bytes.NewReader(out))
	if err != nil {
		t.Fatal(err)
	}
	b := img.Bounds()
	if b.Dx() > CoverSize.X || b.Dy() > CoverSize.Y {
		t.Fatalf("缩略图超出目标框: %dx%d", b.Dx(), b.Dy())
	}
	ratio := float64(b.Dx()) / float64(b.Dy())
	if ratio < 0.6 || ratio > 0.73 { // 原图 800x1200 = 0.667
		t.Fatalf("比例失真: %v (%dx%d)", ratio, b.Dx(), b.Dy())
	}
	if len(out) > 40*1024 {
		t.Fatalf("缩略图过大: %d 字节", len(out))
	}
}

// 缓存体积实测（PRD §6.2 指标口径）。
func TestThumbCacheSizeReport(t *testing.T) {
	var total, spineTotal int
	n := 20
	for i := 0; i < n; i++ {
		cover, err := CoverJPEG(testutil.MakeJPEG(800, 1200, int64(i)))
		if err != nil {
			t.Fatal(err)
		}
		spine, err := SpineJPEG(cover)
		if err != nil {
			t.Fatal(err)
		}
		total += len(cover)
		spineTotal += len(spine)
	}
	coverAvg := total / n
	spineAvg := spineTotal / n
	t.Logf("封面缩略图均值 %.1f KB/本；书脊均值 %.1f KB/本；合计 %.1f KB/本（2 万本约 %.0f MB）",
		float64(coverAvg)/1024, float64(spineAvg)/1024, float64(coverAvg+spineAvg)/1024,
		float64((coverAvg+spineAvg)*20000)/1048576)
	// 预算口径：合成噪声图（JPEG 最坏情况）≤ 22KB/本；真实封面通常小 30% 左右
	if coverAvg+spineAvg > 22*1024 {
		t.Fatalf("缩略图+书脊超过 22KB/本 预算: %d 字节", coverAvg+spineAvg)
	}
}

// TC-M-03：书脊从封面缩略图派生，不重新解码原图。
func TestSpineJPEG(t *testing.T) {
	cover, err := CoverJPEG(testutil.MakeJPEG(800, 1200, 2))
	if err != nil {
		t.Fatal(err)
	}
	spine, err := SpineJPEG(cover)
	if err != nil {
		t.Fatal(err)
	}
	img, err := jpeg.Decode(bytes.NewReader(spine))
	if err != nil {
		t.Fatal(err)
	}
	if img.Bounds().Dx() > SpineSize.X || img.Bounds().Dy() > SpineSize.Y {
		t.Fatalf("书脊超出目标尺寸: %dx%d", img.Bounds().Dx(), img.Bounds().Dy())
	}
}

// 损坏封面字节 → 明确报错，不 panic。
func TestCoverJPEGBadInput(t *testing.T) {
	if _, err := CoverJPEG([]byte("not an image")); err == nil {
		t.Fatal("损坏输入应报错")
	}
}

// TC-M-02：缓存命中不重新生成；同 id 生成只发生一次。
func TestPipelineCacheAndDedupe(t *testing.T) {
	cache := map[string][]byte{}
	gens := 0
	p := NewPipeline()
	p.Load = func(id, kind string) ([]byte, error) {
		if d, ok := cache[id+"/"+kind]; ok {
			return d, nil
		}
		return nil, fmt.Errorf("miss")
	}
	p.Gen = func(id, kind string) ([]byte, error) {
		gens++
		return []byte("data-" + id), nil
	}
	p.Store = func(id, kind string, data []byte) error {
		cache[id+"/"+kind] = data
		return nil
	}

	d1, err := p.Ensure("id1", "cover")
	if err != nil {
		t.Fatal(err)
	}
	d2, err := p.Ensure("id1", "cover")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(d1, d2) {
		t.Fatal("两次取到的数据应一致")
	}
	if gens != 1 {
		t.Fatalf("应只生成一次，实际 %d 次", gens)
	}
}

// 缩放函数：目标框内的比例保持。
func TestScale(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 100, 200))
	out := Scale(src, 50, 50)
	b := out.Bounds()
	if b.Dx() != 25 || b.Dy() != 50 {
		t.Fatalf("缩放结果应为 25x50，得到 %dx%d", b.Dx(), b.Dy())
	}
}
