package parse

import (
	"archive/zip"
	"encoding/xml"
	"fmt"
	"strings"

	"github.com/RexKang/bookrest/internal/domain"
)

type epubParser struct{}

func (epubParser) Match(ext string) bool { return ext == ".epub" }

type container struct {
	Rootfiles []struct {
		FullPath string `xml:"full-path,attr"`
	} `xml:"rootfiles>rootfile"`
}

type opfPackage struct {
	Metadata struct {
		Title   string `xml:"title"`
		Creator string `xml:"creator"`
	} `xml:"metadata"`
	Manifest struct {
		Items []struct {
			ID         string `xml:"id,attr"`
			Href       string `xml:"href,attr"`
			MediaType  string `xml:"media-type,attr"`
			Properties string `xml:"properties,attr"`
		} `xml:"item"`
	} `xml:"manifest"`
}

func (epubParser) Parse(path string) (Result, error) {
	zr, err := zip.OpenReader(path)
	if err != nil {
		return Result{}, fmt.Errorf("%w: %v", ErrCorrupt, err)
	}
	defer zr.Close()

	byName := map[string]*zip.File{}
	for _, f := range zr.File {
		byName[strings.ToLower(f.Name)] = f
	}

	// 1) container.xml → OPF 路径
	var opfPath string
	if cf, ok := byName["meta-inf/container.xml"]; ok {
		if raw, err := readZipFile(cf); err == nil {
			var c container
			if xml.Unmarshal(raw, &c) == nil && len(c.Rootfiles) > 0 {
				opfPath = c.Rootfiles[0].FullPath
			}
		}
	}
	if opfPath == "" {
		for name := range byName {
			if strings.HasSuffix(name, ".opf") {
				opfPath = name
				break
			}
		}
	}

	res := Result{}
	dir := ""
	if opfPath != "" {
		if i := strings.LastIndex(opfPath, "/"); i >= 0 {
			dir = opfPath[:i+1]
		}
		if of, ok := byName[strings.ToLower(opfPath)]; ok {
			if raw, err := readZipFile(of); err == nil {
				var pkg opfPackage
				if xml.Unmarshal(raw, &pkg) == nil {
					res.Meta = domain.Meta{
						Title:  strings.TrimSpace(pkg.Metadata.Title),
						Author: strings.TrimSpace(pkg.Metadata.Creator),
						Src:    domain.SrcOPF,
					}
					res.Embedded = res.Meta.Title != "" || res.Meta.Author != ""
					// 封面：优先 properties="cover-image"，其次 id=cover，其次首个图片
					var href string
					for _, it := range pkg.Manifest.Items {
						if strings.Contains(it.Properties, "cover-image") {
							href = it.Href
							break
						}
					}
					if href == "" {
						for _, it := range pkg.Manifest.Items {
							if it.ID == "cover" || it.ID == "cover-image" {
								href = it.Href
								break
							}
						}
					}
					if href == "" {
						for _, it := range pkg.Manifest.Items {
							if strings.HasPrefix(it.MediaType, "image/") {
								href = it.Href
								break
							}
						}
					}
					if href != "" {
						full := strings.ToLower(dir + href)
						if f, ok := byName[full]; ok {
							if raw, err := readZipFile(f); err == nil {
								res.Cover = raw
								res.CoverExt = strings.ToLower(extOf(href))
							}
						}
					}
				}
			}
		}
	}

	// 页数：统计 xhtml/html 文档数量（EPUB 的「页」语义较弱，仅作参考）
	for name, f := range byName {
		if f.FileInfo().IsDir() {
			continue
		}
		if strings.HasSuffix(name, ".xhtml") || strings.HasSuffix(name, ".html") || strings.HasSuffix(name, ".htm") {
			res.Pages++
		}
	}
	return res, nil
}
