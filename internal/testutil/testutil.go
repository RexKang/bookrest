// Package testutil 提供测试数据构造工具：小体积、确定性、可复现。
// 同时被单元测试与 tools/testdata-gen 使用，保证两边造出的样本一致。
package testutil

import (
	"archive/zip"
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"math/rand"
	"os"
	"path/filepath"
)

// MakeJPEG 生成一张确定性的小图（按种子着色），用于构造测试封面/页面。
func MakeJPEG(w, h int, seed int64) []byte {
	rnd := rand.New(rand.NewSource(seed))
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	base := color.RGBA{R: uint8(rnd.Intn(256)), G: uint8(rnd.Intn(256)), B: uint8(rnd.Intn(256)), A: 255}
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{
				R: uint8((int(base.R) + x*3) % 256),
				G: uint8((int(base.G) + y*5) % 256),
				B: uint8((int(base.B) + x + y) % 256),
				A: 255,
			})
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 70}); err != nil {
		panic(err)
	}
	return buf.Bytes()
}

// CBZOpts 控制构造出来的 CBZ 内容。
type CBZOpts struct {
	Pages        int    // 页数（图片项数）
	PageW, PageH int    // 页尺寸
	ComicInfo    bool   // 是否写 ComicInfo.xml
	Series       string
	Number       string
	Title        string
	Seed         int64
	NoCover      bool   // 不写任何图片（封面缺失场景）
	Corrupt      bool   // 写成截断的 zip（损坏场景）
	ExtraFiles   []string // 额外写入的文本文件（命名地狱等场景）
}

// WriteCBZ 在 path 处写一个 CBZ（zip）。
func WriteCBZ(path string, opts CBZOpts) error {
	if opts.PageW == 0 {
		opts.PageW, opts.PageH = 96, 144
	}
	if opts.Pages == 0 && !opts.NoCover {
		opts.Pages = 6
	}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	if !opts.NoCover {
		for i := 1; i <= opts.Pages; i++ {
			w, err := zw.Create(fmt.Sprintf("%03d.jpg", i))
			if err != nil {
				return err
			}
			if _, err := w.Write(MakeJPEG(opts.PageW, opts.PageH, opts.Seed+int64(i))); err != nil {
				return err
			}
		}
	}
	if opts.ComicInfo {
		w, err := zw.Create("ComicInfo.xml")
		if err != nil {
			return err
		}
		xml := fmt.Sprintf(`<?xml version="1.0"?><ComicInfo><Title>%s</Title><Series>%s</Series><Number>%s</Number><Writer>测试作者</Writer><Penciller>测试画师</Penciller><Year>2020</Year></ComicInfo>`,
			opts.Title, opts.Series, opts.Number)
		if _, err := w.Write([]byte(xml)); err != nil {
			return err
		}
	}
	for _, extra := range opts.ExtraFiles {
		w, err := zw.Create(extra)
		if err != nil {
			return err
		}
		if _, err := w.Write([]byte("x")); err != nil {
			return err
		}
	}
	if err := zw.Close(); err != nil {
		return err
	}
	data := buf.Bytes()
	if opts.Corrupt {
		if len(data) > 64 {
			data = data[:len(data)/3] // 截断：必然打不开
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

// EPUBOpts 控制构造出来的 EPUB。
type EPUBOpts struct {
	Title   string
	Author  string
	WithMeta bool // 是否写 OPF（否则只有 mimetype + 一个章节）
	Pages   int
	Seed    int64
}

// WriteEPUB 在 path 处写一个 EPUB。
func WriteEPUB(path string, opts EPUBOpts) error {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	add := func(name string, data []byte) error {
		w, err := zw.Create(name)
		if err != nil {
			return err
		}
		_, err = w.Write(data)
		return err
	}
	if err := add("mimetype", []byte("application/epub+zip")); err != nil {
		return err
	}
	if err := add("META-INF/container.xml", []byte(`<?xml version="1.0"?><container version="1.0"><rootfiles><rootfile full-path="OEBPS/content.opf" media-type="application/oebps-package+xml"/></rootfiles></container>`)); err != nil {
		return err
	}
	if opts.WithMeta {
		opf := fmt.Sprintf(`<?xml version="1.0"?><package xmlns="http://www.idpf.org/2007/opf" version="3.0"><metadata xmlns:dc="http://purl.org/dc/elements/1.1/"><dc:title>%s</dc:title><dc:creator>%s</dc:creator></metadata><manifest><item id="cover-image" href="cover.jpg" media-type="image/jpeg" properties="cover-image"/><item id="ch1" href="ch1.xhtml" media-type="application/xhtml+xml"/></manifest></package>`, opts.Title, opts.Author)
		if err := add("OEBPS/content.opf", []byte(opf)); err != nil {
			return err
		}
		if err := add("OEBPS/cover.jpg", MakeJPEG(200, 300, opts.Seed)); err != nil {
			return err
		}
	}
	n := opts.Pages
	if n == 0 {
		n = 3
	}
	for i := 1; i <= n; i++ {
		if err := add(fmt.Sprintf("OEBPS/ch%d.xhtml", i), []byte("<html><body><p>测试章节</p></body></html>")); err != nil {
			return err
		}
	}
	if err := zw.Close(); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, buf.Bytes(), 0o644)
}

// FileDigest 返回文件的 (size, mtime, sha256)，用于「源文件零写入」断言。
func FileDigest(path string) (int64, int64, string, error) {
	st, err := os.Stat(path)
	if err != nil {
		return 0, 0, "", err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, 0, "", err
	}
	sum := sha256Hex(data)
	return st.Size(), st.ModTime().Unix(), sum, nil
}
