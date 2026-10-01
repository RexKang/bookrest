package metadata

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Configure 按设置重建 HTTP 客户端：
//
//	""                 跟随系统（Windows 系统代理 → 环境变量 → 直连）
//	"direct"/"none"    直连，不走代理
//	"http://host:port" 指定代理
func Configure(proxy string) { httpClient = buildClient(proxy) }

func buildClient(proxy string) *http.Client {
	t := &http.Transport{}
	if base, ok := http.DefaultTransport.(*http.Transport); ok {
		t = base.Clone()
	}
	switch {
	case strings.EqualFold(proxy, "direct"), strings.EqualFold(proxy, "none"):
		t.Proxy = nil
	case strings.TrimSpace(proxy) != "":
		if u, err := url.Parse(strings.TrimSpace(proxy)); err == nil && u.Host != "" {
			t.Proxy = http.ProxyURL(u)
		}
	default:
		// 跟随系统：优先用 Windows 的系统代理设置（绝大多数本机代理软件只写这里，
		// 不设环境变量），没有则退回环境变量
		if p := systemProxy(); p != "" {
			if u, err := url.Parse(p); err == nil && u.Host != "" {
				t.Proxy = http.ProxyURL(u)
				break
			}
		}
		t.Proxy = http.ProxyFromEnvironment
	}
	return &http.Client{Timeout: 15 * time.Second, Transport: t}
}

// postJSON 发一个 JSON POST（Bangumi 等源用）。
func postJSON(ctx context.Context, rawURL string, payload any, out any) error {
	if !HostAllowed(rawURL) {
		return errHostNotAllowed(rawURL)
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, rawURL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", userAgent)
	resp, err := httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return errStatus(rawURL, resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return err
	}
	return json.Unmarshal(data, out)
}

const userAgent = "Bookrest/0.1 (local-first bookshelf; +https://github.com/RexKang/bookrest)"

// ---------- Bangumi（中文漫画/轻小说/动画，国内可达） ----------

type bangumi struct{}

func (bangumi) Name() string { return "Bangumi" }

func (bangumi) Search(ctx context.Context, q Query, limit int) ([]Candidate, error) {
	text := q.Text()
	if text == "" {
		return nil, errEmptyQuery()
	}
	if limit <= 0 || limit > 20 {
		limit = 8
	}
	u := bangumiURL + "?limit=" + strconv.Itoa(limit) + "&offset=0"
	var payload struct {
		Data []struct {
			ID     int    `json:"id"`
			Name   string `json:"name"`
			NameCN string `json:"name_cn"`
			Date   string `json:"date"`
			Images struct {
				Large  string `json:"large"`
				Common string `json:"common"`
				Medium string `json:"medium"`
			} `json:"images"`
			Infobox []struct {
				Key   string `json:"key"`
				Value any    `json:"value"`
			} `json:"infobox"`
		} `json:"data"`
	}
	if err := postJSON(ctx, u, map[string]any{"keyword": text}, &payload); err != nil {
		return nil, err
	}
	out := make([]Candidate, 0, len(payload.Data))
	for _, d := range payload.Data {
		title := d.NameCN
		if strings.TrimSpace(title) == "" {
			title = d.Name
		}
		cover := d.Images.Large
		if cover == "" {
			cover = d.Images.Common
		}
		if cover == "" {
			cover = d.Images.Medium
		}
		if strings.HasPrefix(cover, "http://") {
			cover = "https://" + strings.TrimPrefix(cover, "http://")
		}
		c := Candidate{
			Source: "Bangumi", Title: title,
			CoverURL: cover, Detail: "bgm:" + strconv.Itoa(d.ID),
		}
		if len(d.Date) >= 4 {
			c.Year = d.Date[:4]
		}
		for _, ib := range d.Infobox {
			switch strings.ToLower(ib.Key) {
			case "册数":
				c.Number = infoboxText(ib.Value)
			case "出版社", "连载杂志":
				if c.Publisher == "" {
					c.Publisher = infoboxText(ib.Value)
				}
			case "作者":
				if c.Author == "" {
					c.Author = infoboxText(ib.Value)
				}
			}
		}
		c.Score = score(q, c)
		out = append(out, c)
	}
	return out, nil
}

// infoboxText 把 infobox 的 value（字符串或字符串数组）转成文本。
func infoboxText(v any) string {
	switch t := v.(type) {
	case string:
		return strings.TrimSpace(t)
	case []any:
		parts := make([]string, 0, len(t))
		for _, x := range t {
			if s, ok := x.(string); ok {
				parts = append(parts, strings.TrimSpace(s))
			}
		}
		return strings.Join(parts, "、")
	}
	return ""
}
