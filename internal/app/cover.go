package app

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/RexKang/bookrest/internal/parse"
	"github.com/RexKang/bookrest/internal/store"
	"github.com/RexKang/bookrest/internal/thumb"
)

// maxCoverFile 是封面图片的体积上限（防止误选几十 MB 的扫描件）。
const maxCoverFile = 16 << 20

// coverDir 是本机缓存里放「用户指定封面」的目录（派生数据，可整体删除）。
func (a *App) coverDir() string { return filepath.Join(a.shelfDir(), "covers") }

// SetCoverFromData 用粘贴/上传的图片设置封面；data 可以是 data URL，也可以是裸 base64。
func (a *App) SetCoverFromData(id, data, source string) error {
	raw, err := decodeImagePayload(data)
	if err != nil {
		return err
	}
	if strings.TrimSpace(source) == "" {
		source = "手动粘贴"
	}
	return a.saveCover(id, raw, source, "")
}

// SetCoverFromFile 从本地图片文件设置封面（配合原生文件选择器）。
func (a *App) SetCoverFromFile(id, path string) error {
	fi, err := os.Stat(path)
	if err != nil {
		return err
	}
	if fi.IsDir() {
		return fmt.Errorf("这是一个目录：%s", path)
	}
	if fi.Size() > maxCoverFile {
		return fmt.Errorf("图片太大（%.1fMB，上限 %dMB）", float64(fi.Size())/(1<<20), maxCoverFile>>20)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return a.saveCover(id, raw, "本地文件", "")
}

// ClearCover 清除用户指定封面，回退到文件内嵌封面（或占位图）。
func (a *App) ClearCover(id string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	ov := a.shelf.Overrides[id]
	if ov.Cover == nil {
		return nil
	}
	_ = os.Remove(filepath.Join(a.coverDir(), ov.Cover.File))
	ov.Cover = nil
	a.shelf.Overrides[id] = ov
	delete(a.thumbs, id)
	return a.saveShelfLocked()
}

// CoverBytes 返回详情页用的大图（用户封面优先，其次文件内嵌封面）。
func (a *App) CoverBytes(id string) ([]byte, error) {
	raw, err := a.rawCover(id)
	if err != nil {
		return nil, err
	}
	return thumb.CoverJPEGSize(raw, 480, 720)
}

// rawCover 取封面的原始字节：用户指定的优先，否则从文件里解析。
func (a *App) rawCover(id string) ([]byte, error) {
	a.mu.Lock()
	ov := a.shelf.Overrides[id]
	dir := a.coverDir()
	f, ok := a.idx.Get(id)
	root := a.root
	a.mu.Unlock()
	if !ok {
		return nil, fmt.Errorf("未知条目：%s", id)
	}
	if ov.Cover != nil {
		if raw, err := os.ReadFile(filepath.Join(dir, ov.Cover.File)); err == nil {
			return raw, nil
		}
		// 缓存文件丢了（比如手工清了缓存）→ 退回内嵌封面，不报错打断界面
	}
	return embeddedCover(f, root)
}

// embeddedCover 从书籍文件里解析内嵌封面（只读）。
func embeddedCover(f store.Fact, root string) ([]byte, error) {
	p, ok := parse.ForPath(f.Rel)
	if !ok {
		return nil, fmt.Errorf("该格式不支持提取封面")
	}
	res, err := p.Parse(filepath.Join(root, filepath.FromSlash(f.Rel)))
	if err != nil {
		return nil, err
	}
	if len(res.Cover) == 0 {
		return nil, fmt.Errorf("无封面")
	}
	return res.Cover, nil
}

// saveCover 落盘一张用户封面并写进意图账。
func (a *App) saveCover(id string, raw []byte, source, query string) error {
	cfg, format, err := image.DecodeConfig(bytes.NewReader(raw))
	if err != nil {
		return fmt.Errorf("不是有效的图片：%w", err)
	}
	if cfg.Width < 16 || cfg.Height < 16 {
		return fmt.Errorf("图片太小（%dx%d）", cfg.Width, cfg.Height)
	}
	ext := extForFormat(format)
	if ext == "" {
		return fmt.Errorf("不支持的图片格式：%s", format)
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if _, ok := a.idx.Get(id); !ok {
		return fmt.Errorf("未知条目：%s", id)
	}
	dir := a.coverDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	name := id + ext
	if err := os.WriteFile(filepath.Join(dir, name), raw, 0o644); err != nil {
		return err
	}
	old := a.shelf.Overrides[id]
	if old.Cover != nil && old.Cover.File != name {
		_ = os.Remove(filepath.Join(dir, old.Cover.File))
	}
	old.Cover = &store.CoverRef{
		File: name, Ext: ext, Source: source, Query: query, At: time.Now().Unix(),
	}
	a.shelf.Overrides[id] = old
	delete(a.thumbs, id) // 缩略图缓存失效，下次重新生成
	return a.saveShelfLocked()
}

func extForFormat(format string) string {
	switch strings.ToLower(format) {
	case "jpeg":
		return ".jpg"
	case "png":
		return ".png"
	case "gif":
		return ".gif"
	case "webp":
		return ".webp"
	}
	return ""
}

// decodeImagePayload 解析 data URL 或裸 base64。
func decodeImagePayload(data string) ([]byte, error) {
	s := strings.TrimSpace(data)
	if i := strings.Index(s, ","); strings.HasPrefix(s, "data:") && i > 0 {
		s = s[i+1:]
	}
	raw, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		// 兼容 URL-safe / 无填充的变体
		raw, err = base64.RawStdEncoding.DecodeString(strings.TrimRight(s, "="))
		if err != nil {
			return nil, fmt.Errorf("图片数据不是有效的 base64：%w", err)
		}
	}
	if len(raw) == 0 {
		return nil, fmt.Errorf("图片数据为空")
	}
	if len(raw) > maxCoverFile {
		return nil, fmt.Errorf("图片太大（%.1fMB，上限 %dMB）", float64(len(raw))/(1<<20), maxCoverFile>>20)
	}
	return raw, nil
}
