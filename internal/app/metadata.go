package app

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/RexKang/bookrest/internal/metadata"
	"github.com/RexKang/bookrest/internal/store"
	"github.com/RexKang/bookrest/internal/thumb"
)

// candidateDir 是「联网搜到的候选封面」的临时缓存目录（派生数据，可删）。
func (a *App) candidateDir() string { return filepath.Join(a.coverDir(), "cand") }

// SearchMetadata 按用户点击的查询词联网检索候选信息。
// 只发送书目字段（书名/系列/卷号/作者），不发送路径、文件或指纹。
func (a *App) SearchMetadata(id, query string, sources []string) ([]metadata.Candidate, error) {
	a.mu.Lock()
	f, ok := a.idx.Get(id)
	ov := a.shelf.Overrides[id]
	a.mu.Unlock()
	if !ok {
		return nil, fmt.Errorf("未知条目：%s", id)
	}

	q := metadata.Query{
		Title:  firstNonEmpty(ov.Title, f.Meta.Title),
		Series: firstNonEmpty(ov.Series, f.Meta.Series),
		Number: firstNonEmpty(ov.Number, f.Meta.Number),
		Author: firstNonEmpty(ov.Author, f.Meta.Author),
	}
	if s := strings.TrimSpace(query); s != "" {
		q = metadata.Query{Title: s} // 用户手填的查询词优先
	}
	if q.Text() == "" {
		return nil, fmt.Errorf("这本书没有可用的书名/系列，请手动填写查询词")
	}

	srcs := metadata.Sources(sources)
	if len(srcs) == 0 {
		return nil, fmt.Errorf("没有可用的数据源")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()

	// 各源并行查询（互不阻塞，单个源慢不影响其它源）
	type res struct {
		cs  []metadata.Candidate
		err error
		name string
	}
	ch := make(chan res, len(srcs))
	for _, s := range srcs {
		go func(s metadata.Source) {
			cs, err := s.Search(ctx, q, 8)
			ch <- res{cs: cs, err: err, name: s.Name()}
		}(s)
	}
	var out []metadata.Candidate
	var errs []string
	for i := 0; i < len(srcs); i++ {
		r := <-ch
		if r.err != nil {
			errs = append(errs, r.name+"："+r.err.Error())
			continue
		}
		out = append(out, r.cs...)
	}
	if len(out) == 0 && len(errs) > 0 {
		return nil, fmt.Errorf("查询失败：%s", strings.Join(errs, "；"))
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Score > out[j].Score })

	// 封面下载到本机缓存（不热链；失败不影响结果本身）。并发下载，别让界面等一串超时。
	sem := make(chan struct{}, 4)
	var wg sync.WaitGroup
	for i := range out {
		if out[i].CoverURL == "" {
			continue
		}
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			if key, err := a.cacheCandidateCover(out[i].CoverURL); err == nil {
				out[i].CoverKey = key
			}
		}(i)
	}
	wg.Wait()
	if len(out) > 30 {
		out = out[:30]
	}
	return out, nil
}

// cacheCandidateCover 下载候选封面并归一化成 JPEG 缓存，返回缓存标识。
func (a *App) cacheCandidateCover(rawURL string) (string, error) {
	if !metadata.HostAllowed(rawURL) {
		return "", fmt.Errorf("封面域名不在白名单内")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	raw, err := metadata.FetchBytes(ctx, rawURL, 8<<20)
	if err != nil {
		return "", err
	}
	jpg, err := thumb.CoverJPEGSize(raw, 480, 720) // 顺带校验确实是图片
	if err != nil {
		return "", err
	}
	sum := sha1.Sum([]byte(rawURL))
	key := hex.EncodeToString(sum[:])[:16]
	dir := a.candidateDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(dir, key+".jpg"), jpg, 0o644); err != nil {
		return "", err
	}
	return key, nil
}

// CandidateCover 读取候选封面缓存（供界面预览）。
func (a *App) CandidateCover(key string) ([]byte, error) {
	if key == "" || strings.ContainsAny(key, `/\`) || strings.Contains(key, "..") {
		return nil, fmt.Errorf("非法的封面标识")
	}
	return os.ReadFile(filepath.Join(a.candidateDir(), key+".jpg"))
}

// ApplyCandidate 把选中的候选写入意图账；fields 为空表示「全部非空字段 + 封面」。
// 用户手动填写/粘贴的内容始终可以覆盖回来——这里写的同样是 override，不改源文件。
func (a *App) ApplyCandidate(id string, cand metadata.Candidate, fields []string) error {
	want := map[string]bool{}
	for _, f := range fields {
		want[strings.ToLower(strings.TrimSpace(f))] = true
	}
	all := len(want) == 0
	pick := func(name string) bool { return all || want[name] }

	a.mu.Lock()
	defer a.mu.Unlock()
	if _, ok := a.idx.Get(id); !ok {
		return fmt.Errorf("未知条目：%s", id)
	}
	ov := a.shelf.Overrides[id]
	if pick("title") && cand.Title != "" {
		ov.Title = cand.Title
	}
	if pick("author") && cand.Author != "" {
		ov.Author = cand.Author
	}
	if pick("series") && cand.Series != "" {
		ov.Series = cand.Series
	}
	if pick("number") && cand.Number != "" {
		ov.Number = cand.Number
	}
	if pick("cover") && cand.CoverKey != "" {
		if ref, err := a.promoteCandidateCover(cand.CoverKey, id, cand.Source, cand.Score); err == nil {
			ov.Cover = ref
		}
	}
	a.shelf.Overrides[id] = ov
	delete(a.thumbs, id)
	return a.saveShelfLocked()
}

// promoteCandidateCover 把候选封面从临时缓存转正为条目的用户封面。
func (a *App) promoteCandidateCover(key, id, source string, score float64) (*store.CoverRef, error) {
	src := filepath.Join(a.candidateDir(), key+".jpg")
	raw, err := os.ReadFile(src)
	if err != nil {
		return nil, err
	}
	dir := a.coverDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	name := id + ".jpg"
	if err := os.WriteFile(filepath.Join(dir, name), raw, 0o644); err != nil {
		return nil, err
	}
	old := a.shelf.Overrides[id]
	if old.Cover != nil && old.Cover.File != name {
		_ = os.Remove(filepath.Join(dir, old.Cover.File))
	}
	return &store.CoverRef{
		File: name, Ext: ".jpg", Source: source, Query: "score=" + fmt.Sprintf("%.2f", score),
		At: time.Now().Unix(),
	}, nil
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
