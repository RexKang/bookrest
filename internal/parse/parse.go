// Package parse 负责从文件里读出「事实」：内嵌元数据、页数、封面字节。
// 全部操作只读，绝不写回源文件。
package parse

import (
	"errors"
	"path/filepath"
	"strings"

	"github.com/RexKang/bookrest/internal/domain"
)

var (
	// ErrCorrupt 表示归档结构损坏（截断、CRC 错、伪装扩展名等）。
	ErrCorrupt = errors.New("parse: corrupt archive")
	// ErrUnsupported 表示该格式当前不支持解析（仅索引）。
	ErrUnsupported = errors.New("parse: unsupported format")
)

// Result 是一次解析的产物。
type Result struct {
	Meta     domain.Meta
	Pages    int    // 图片项数量；未知为 0
	Cover    []byte // 首个图片项的原始字节；无封面为 nil
	CoverExt string // 封面原始扩展名（.jpg 等）
	Embedded bool   // 是否读到内嵌元数据
}

// Parser 解析一种扩展名。
type Parser interface {
	Match(ext string) bool
	Parse(path string) (Result, error)
}

var registry = []Parser{cbzParser{}, epubParser{}}

// For 返回匹配该扩展名的解析器（ext 形如 ".cbz"）。
func For(ext string) (Parser, bool) {
	ext = strings.ToLower(ext)
	for _, p := range registry {
		if p.Match(ext) {
			return p, true
		}
	}
	return nil, false
}

// ForPath 便捷入口。
func ForPath(path string) (Parser, bool) { return For(filepath.Ext(path)) }

// bookExts 是「算作书」的扩展名白名单。
// 扫描器只索引这些格式，其余文件（.json/.txt/.nfo 等）一律忽略，避免把无关文件当书。
var bookExts = map[string]bool{
	".cbz": true, ".cbr": true, ".epub": true, ".pdf": true,
	".mobi": true, ".azw3": true, ".azw": true, ".fb2": true,
}

// IsBookExt 判断扩展名是否属于受支持的书籍格式。
func IsBookExt(ext string) bool { return bookExts[strings.ToLower(ext)] }

// CanExtractCover 判断该格式是否能自动提取封面（即有没有对应解析器）。
// PDF/CBR/MOBI 这类只索引的格式天然取不到封面，体检与界面据此区分「不支持」与「缺封面」。
func CanExtractCover(ext string) bool {
	_, ok := For(strings.ToLower(ext))
	return ok
}

// IsImageExt 判断是否算作「页」。
func IsImageExt(name string) bool {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".jpg", ".jpeg", ".png", ".webp", ".gif", ".bmp", ".avif":
		return true
	}
	return false
}
