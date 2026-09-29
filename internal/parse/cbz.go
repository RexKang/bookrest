package parse

import (
	"archive/zip"
	"encoding/xml"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"

	"github.com/RexKang/bookrest/internal/domain"
)

type cbzParser struct{}

func (cbzParser) Match(ext string) bool { return ext == ".cbz" || ext == ".zip" }

// comicInfo 对应 ComicRack 的 ComicInfo.xml 标准（Mihon/Komga 都读它）。
type comicInfo struct {
	XMLName  xml.Name `xml:"ComicInfo"`
	Title    string   `xml:"Title"`
	Series   string   `xml:"Series"`
	Number   string   `xml:"Number"`
	Writer   string   `xml:"Writer"`
	Penciler string   `xml:"Penciller"`
	Year     string   `xml:"Year"`
}

func (cbzParser) Parse(path string) (Result, error) {
	zr, err := zip.OpenReader(path)
	if err != nil {
		return Result{}, fmt.Errorf("%w: %v", ErrCorrupt, err)
	}
	defer zr.Close()

	var images []*zip.File
	var info *zip.File
	for _, f := range zr.File {
		if f.FileInfo().IsDir() {
			continue
		}
		switch {
		case strings.EqualFold(f.Name, "ComicInfo.xml"):
			info = f
		case IsImageExt(f.Name):
			images = append(images, f)
		}
	}
	if len(images) == 0 && info == nil {
		return Result{}, fmt.Errorf("%w: no images and no ComicInfo", ErrCorrupt)
	}

	res := Result{Pages: len(images)}
	if info != nil {
		if raw, err := readZipFile(info); err == nil {
			var ci comicInfo
			if xml.Unmarshal(raw, &ci) == nil {
				res.Meta = domain.Meta{
					Title:  strings.TrimSpace(ci.Title),
					Series: strings.TrimSpace(ci.Series),
					Number: strings.TrimSpace(ci.Number),
					Author: strings.TrimSpace(ci.Writer),
					Artist: strings.TrimSpace(ci.Penciler),
					Src:    domain.SrcComicInfo,
				}
				if y, err := strconv.Atoi(strings.TrimSpace(ci.Year)); err == nil {
					res.Meta.Year = y
				}
				res.Embedded = res.Meta.Title != "" || res.Meta.Series != "" || res.Meta.Author != ""
			}
		}
	}
	if len(images) > 0 {
		sort.Slice(images, func(i, j int) bool { return naturalLess(images[i].Name, images[j].Name) })
		first := images[0]
		if raw, err := readZipFile(first); err == nil {
			res.Cover = raw
			res.CoverExt = strings.ToLower(extOf(first.Name))
		}
	}
	return res, nil
}

func readZipFile(f *zip.File) ([]byte, error) {
	rc, err := f.Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	const maxCover = 32 << 20 // 32MB 上限，防异常大文件
	return io.ReadAll(io.LimitReader(rc, maxCover))
}

func extOf(name string) string {
	if i := strings.LastIndex(name, "."); i >= 0 {
		return name[i:]
	}
	return ""
}

// naturalLess 按「数字段数值比较」排序，让 2.jpg 排在 10.jpg 之前。
func naturalLess(a, b string) bool {
	na, nb := splitNums(a), splitNums(b)
	for i := 0; i < len(na) && i < len(nb); i++ {
		xa, xb := na[i], nb[i]
		if xa == xb {
			continue
		}
		fa, ea := strconv.Atoi(xa)
		fb, eb := strconv.Atoi(xb)
		if ea == nil && eb == nil {
			return fa < fb
		}
		return xa < xb
	}
	return len(na) < len(nb)
}

func splitNums(s string) []string {
	var out []string
	var cur strings.Builder
	digit := false
	for _, r := range s {
		isDigit := r >= '0' && r <= '9'
		if cur.Len() > 0 && isDigit != digit {
			out = append(out, cur.String())
			cur.Reset()
		}
		digit = isDigit
		cur.WriteRune(r)
	}
	if cur.Len() > 0 {
		out = append(out, cur.String())
	}
	return out
}
