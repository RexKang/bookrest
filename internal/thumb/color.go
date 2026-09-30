package thumb

import (
	"bytes"
	"image"
	"image/color"
)

// AverageColor 计算封面主色（缩到 8×8 后取均值），用于无封面占位块与书脊配色。
func AverageColor(raw []byte) (color.RGBA, error) {
	img, _, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		return color.RGBA{}, err
	}
	small := Scale(img, 8, 8)
	b := small.Bounds()
	var rs, gs, bs, n uint64
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			r, g, bl, a := small.At(x, y).RGBA()
			if a == 0 {
				continue
			}
			rs += uint64(r >> 8)
			gs += uint64(g >> 8)
			bs += uint64(bl >> 8)
			n++
		}
	}
	if n == 0 {
		return color.RGBA{64, 64, 64, 255}, nil
	}
	return color.RGBA{R: uint8(rs / n), G: uint8(gs / n), B: uint8(bs / n), A: 255}, nil
}
