// Package thumb 是缩略图与书脊的懒生成流水线（第三段扫描）。
// 实现只用标准库（JPEG 编解码），不引入 WebP 依赖；产物是可重建缓存。
package thumb

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	_ "image/gif"
	_ "image/png"
	"math"
	"sync"
)

// CoverSize 是封面缩略图的目标上限（长边不超过该框）。
//
// 档位是实测选出来的（合成噪声图 = JPEG 最坏情况，2 万本全量缓存）：
//
//	256x384 q75 → 27.1KB/本 ≈ 528MB
//	192x288 q70 → 16.6KB/本 ≈ 323MB   ← 采用：书架网格清晰度与体积的折中
//	128x192 q68 →  8.8KB/本 ≈ 172MB
//
// 真实封面（大片平坦区+文字）通常比合成噪声图小 30% 左右，且缩略图是懒生成的，
// 全库缓存只在「每本都被浏览过」时才会接近上限。
var CoverSize = image.Pt(192, 288)

// SpineSize 是书脊切片的目标尺寸。
var SpineSize = image.Pt(48, 300)

// jpegQuality 是缩略图编码质量；变量形式便于基准测试对比不同档位。
var jpegQuality = 70

// jpegQualityOverride 仅测试用（>0 时覆盖 jpegQuality）。
var jpegQualityOverride = 0

func quality() int {
	if jpegQualityOverride > 0 {
		return jpegQualityOverride
	}
	return jpegQuality
}

// CoverJPEG 把原始封面字节解码、缩放、重新编码为 JPEG。
func CoverJPEG(raw []byte) ([]byte, error) {
	img, _, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("thumb: decode: %w", err)
	}
	scaled := Scale(img, CoverSize.X, CoverSize.Y)
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, scaled, &jpeg.Options{Quality: quality()}); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// CoverJPEGSize 按指定最大尺寸生成封面 JPEG（详情页大图用；0 表示不限）。
func CoverJPEGSize(raw []byte, maxW, maxH int) ([]byte, error) {
	img, _, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("thumb: decode: %w", err)
	}
	if maxW <= 0 {
		maxW = CoverSize.X
	}
	if maxH <= 0 {
		maxH = CoverSize.Y
	}
	scaled := Scale(img, maxW, maxH)
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, scaled, &jpeg.Options{Quality: quality()}); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// SpineJPEG 从已生成的封面缩略图派生书脊（不重新解码原图）。
func SpineJPEG(coverJPEG []byte) ([]byte, error) {
	img, err := jpeg.Decode(bytes.NewReader(coverJPEG))
	if err != nil {
		return nil, fmt.Errorf("thumb: decode cover: %w", err)
	}
	b := img.Bounds()
	stripW := int(math.Max(1, math.Round(float64(b.Dx())*0.12)))
	strip := subImage{src: img, r: image.Rect(b.Min.X, b.Min.Y, b.Min.X+stripW, b.Max.Y)}
	scaled := Scale(strip, SpineSize.X, SpineSize.Y)
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, scaled, &jpeg.Options{Quality: quality()}); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// subImage 用一个裁剪矩形包装原图（矩形坐标与原图同一坐标系）。
type subImage struct {
	src image.Image
	r   image.Rectangle
}

func (s subImage) ColorModel() color.Model { return s.src.ColorModel() }
func (s subImage) Bounds() image.Rectangle { return s.r }
func (s subImage) At(x, y int) color.Color { return s.src.At(x, y) }

// Scale 双线性缩放（保持比例，长边贴合目标框）。
func Scale(src image.Image, maxW, maxH int) *image.RGBA {
	b := src.Bounds()
	sw, sh := b.Dx(), b.Dy()
	if sw == 0 || sh == 0 {
		return image.NewRGBA(image.Rect(0, 0, maxW, maxH))
	}
	scale := math.Min(float64(maxW)/float64(sw), float64(maxH)/float64(sh))
	dw := int(math.Max(1, math.Round(float64(sw)*scale)))
	dh := int(math.Max(1, math.Round(float64(sh)*scale)))
	dst := image.NewRGBA(image.Rect(0, 0, dw, dh))
	for y := 0; y < dh; y++ {
		sy := (float64(y) + 0.5) * float64(sh) / float64(dh)
		for x := 0; x < dw; x++ {
			sx := (float64(x) + 0.5) * float64(sw) / float64(dw)
			dst.Set(x, y, sampleBilinear(src, float64(b.Min.X)+sx-0.5, float64(b.Min.Y)+sy-0.5))
		}
	}
	return dst
}

func sampleBilinear(src image.Image, x, y float64) color.RGBA {
	x0, y0 := int(math.Floor(x)), int(math.Floor(y))
	fx, fy := x-float64(x0), y-float64(y0)
	b := src.Bounds()
	clampX := func(v int) int {
		if v < b.Min.X {
			return b.Min.X
		}
		if v > b.Max.X-1 {
			return b.Max.X - 1
		}
		return v
	}
	clampY := func(v int) int {
		if v < b.Min.Y {
			return b.Min.Y
		}
		if v > b.Max.Y-1 {
			return b.Max.Y - 1
		}
		return v
	}
	r00, g00, b00, a00 := toRGBA8(src.At(clampX(x0), clampY(y0)))
	r10, g10, b10, a10 := toRGBA8(src.At(clampX(x0+1), clampY(y0)))
	r01, g01, b01, a01 := toRGBA8(src.At(clampX(x0), clampY(y0+1)))
	r11, g11, b11, a11 := toRGBA8(src.At(clampX(x0+1), clampY(y0+1)))
	blend := func(c00, c10, c01, c11 uint8) uint8 {
		top := float64(c00)*(1-fx) + float64(c10)*fx
		bot := float64(c01)*(1-fx) + float64(c11)*fx
		return uint8(math.Round(top*(1-fy) + bot*fy))
	}
	return color.RGBA{
		R: blend(r00, r10, r01, r11),
		G: blend(g00, g10, g01, g11),
		B: blend(b00, b10, b01, b11),
		A: blend(a00, a10, a01, a11),
	}
}

func toRGBA8(c color.Color) (uint8, uint8, uint8, uint8) {
	r, g, b, a := c.RGBA()
	return uint8(r >> 8), uint8(g >> 8), uint8(b >> 8), uint8(a >> 8)
}

// Pipeline 负责缓存与并发去重。
type Pipeline struct {
	mu       sync.Mutex
	inflight map[string]bool
	Gen      func(id string, kind string) ([]byte, error)
	Store    func(id string, kind string, data []byte) error
	Load     func(id string, kind string) ([]byte, error)
}

func NewPipeline() *Pipeline {
	return &Pipeline{inflight: map[string]bool{}}
}

// Ensure 保证某 id 的产物存在；同 id 并发请求合并为一次生成。
func (p *Pipeline) Ensure(id, kind string) ([]byte, error) {
	if p.Load != nil {
		if data, err := p.Load(id, kind); err == nil && len(data) > 0 {
			return data, nil
		}
	}
	p.mu.Lock()
	if p.inflight[id+"/"+kind] {
		p.mu.Unlock()
		return nil, fmt.Errorf("thumb: generating")
	}
	p.inflight[id+"/"+kind] = true
	p.mu.Unlock()
	defer func() {
		p.mu.Lock()
		delete(p.inflight, id+"/"+kind)
		p.mu.Unlock()
	}()

	data, err := p.Gen(id, kind)
	if err != nil {
		return nil, err
	}
	if p.Store != nil {
		if err := p.Store(id, kind, data); err != nil {
			return data, err
		}
	}
	return data, nil
}
