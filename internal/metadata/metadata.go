// Package metadata 负责「在线补全书籍信息」：按需查询外部书目源，返回候选供用户挑选。
//
// 设计约束（见 docs 设计文档 §8）：
//   - 只在用户主动点「搜索信息」时联网，绝不在扫描/启动时联网
//   - 只发送书名/系列/卷号/作者这类书目字段，不发送路径、不发送文件、不发送指纹
//   - 目标域名固定白名单，不接受任意 URL
//   - 返回的封面由本包下载到本机缓存（派生数据，可删），不热链
package metadata

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Query 是一次查询的输入（全部来自书目字段，不含路径）。
type Query struct {
	Title  string // 书名（优先）
	Series string // 系列名
	Number string // 卷号
	Author string // 作者
}

// Text 拼出用于检索的字符串。
func (q Query) Text() string {
	parts := []string{}
	base := strings.TrimSpace(q.Title)
	if base == "" {
		base = strings.TrimSpace(q.Series)
		if q.Number != "" {
			base = strings.TrimSpace(base + " " + q.Number)
		}
	}
	if base != "" {
		parts = append(parts, base)
	}
	if q.Author != "" {
		parts = append(parts, q.Author)
	}
	return strings.Join(parts, " ")
}

// Candidate 是一个候选结果。
type Candidate struct {
	Source    string  `json:"source"`
	Title     string  `json:"title,omitempty"`
	Series    string  `json:"series,omitempty"`
	Number    string  `json:"number,omitempty"`
	Author    string  `json:"author,omitempty"`
	Year      string  `json:"year,omitempty"`
	Publisher string  `json:"publisher,omitempty"`
	CoverURL  string  `json:"coverUrl,omitempty"`
	CoverKey  string  `json:"coverKey,omitempty"` // 后端已缓存到本机的封面标识
	Score     float64 `json:"score"`              // 与查询词的贴合度（0-1），用于排序与置信判断
	Detail    string  `json:"detail,omitempty"`   // 来源内标识（如 ISBN / OpenLibrary key）
}

// Source 是一个书目源。
type Source interface {
	Name() string
	Search(ctx context.Context, q Query, limit int) ([]Candidate, error)
}

// 允许访问的域名白名单（同时用于封面下载，防 SSRF）。
var allowedHosts = map[string]bool{
	"www.googleapis.com":      true,
	"books.google.com":        true,
	"openlibrary.org":         true,
	"covers.openlibrary.org":  true,
	"api.bgm.tv":              true,
	"lain.bgm.tv":             true,
}

// testAllowInsecure 仅供测试使用：放行 httptest 的 http://127.0.0.1 服务器。
var testAllowInsecure = false

// HostAllowed 判断域名是否在白名单内（且必须是 https）。
func HostAllowed(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	if testAllowInsecure {
		return u.Scheme == "http" || u.Scheme == "https"
	}
	if u.Scheme != "https" {
		return false
	}
	return allowedHosts[strings.ToLower(u.Hostname())]
}

// Sources 返回内置源；names 为空则返回全部。
func Sources(names []string) []Source {
	all := []Source{googleBooks{}, openLibrary{}, bangumi{}}
	if len(names) == 0 {
		return all
	}
	want := map[string]bool{}
	for _, n := range names {
		want[strings.ToLower(strings.TrimSpace(n))] = true
	}
	out := []Source{}
	for _, s := range all {
		if want[strings.ToLower(s.Name())] {
			out = append(out, s)
		}
	}
	return out
}

// AvailableSourceNames 列出内置源名（供界面显示）。
func AvailableSourceNames() []string { return []string{"Google Books", "Open Library", "Bangumi"} }

var httpClient = &http.Client{Timeout: 12 * time.Second}

// 基址单独提出来，便于测试注入；生产值是固定白名单域名。
var (
	googleBooksURL = "https://www.googleapis.com/books/v1/volumes"
	openLibraryURL = "https://openlibrary.org/search.json"
	bangumiURL     = "https://api.bgm.tv/v0/search/subjects"
)

// FetchBytes 用**配置好的客户端**（含代理）下载一个白名单内的资源。
// 注意：不要用 http.DefaultClient —— 它不读系统代理设置，会直连超时。
func FetchBytes(ctx context.Context, rawURL string, maxBytes int64) ([]byte, error) {
	if !HostAllowed(rawURL) {
		return nil, errHostNotAllowed(rawURL)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", userAgent)
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, errStatus(rawURL, resp.StatusCode)
	}
	if maxBytes <= 0 {
		maxBytes = 8 << 20
	}
	return io.ReadAll(io.LimitReader(resp.Body, maxBytes))
}

func errHostNotAllowed(u string) error { return fmt.Errorf("域名不在白名单内：%s", u) }
func errStatus(u string, code int) error { return fmt.Errorf("%s 返回 %d", u, code) }
func errEmptyQuery() error             { return fmt.Errorf("查询词为空") }

// normalizeProxyServer 处理 "host:port" 与 "http=h:p;https=h:p" 两种写法。
// 放在与平台无关的文件里：Windows 读注册表时用它，测试也直接覆盖它。
func normalizeProxyServer(server string) string {
	server = strings.TrimSpace(server)
	if server == "" {
		return ""
	}
	if strings.Contains(server, "=") {
		pick := ""
		for _, part := range strings.Split(server, ";") {
			kv := strings.SplitN(part, "=", 2)
			if len(kv) != 2 {
				continue
			}
			if strings.EqualFold(strings.TrimSpace(kv[0]), "https") {
				pick = strings.TrimSpace(kv[1])
				break
			}
			if pick == "" {
				pick = strings.TrimSpace(kv[1])
			}
		}
		server = pick
	}
	if server == "" {
		return ""
	}
	if !strings.Contains(server, "://") {
		server = "http://" + server
	}
	return server
}

func getJSON(ctx context.Context, rawURL string, out any) error {
	if !HostAllowed(rawURL) {
		return fmt.Errorf("域名不在白名单内：%s", rawURL)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return err
	}
	// 明确标识自己（Open Library / Google 都建议带可联系的 UA）
	req.Header.Set("User-Agent", userAgent)
	resp, err := httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return errStatus(rawURL, resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return err
	}
	return json.Unmarshal(body, out)
}

// ---------- Google Books ----------

type googleBooks struct{}

func (googleBooks) Name() string { return "Google Books" }

func (googleBooks) Search(ctx context.Context, q Query, limit int) ([]Candidate, error) {
	text := q.Text()
	if text == "" {
		return nil, errEmptyQuery()
	}
	if limit <= 0 || limit > 20 {
		limit = 8
	}
	u := googleBooksURL + "?q=" + url.QueryEscape(text) +
		"&maxResults=" + strconv.Itoa(limit) + "&printType=books"
	var payload struct {
		Items []struct {
			ID         string `json:"id"`
			VolumeInfo struct {
				Title         string   `json:"title"`
				Subtitle      string   `json:"subtitle"`
				Authors       []string `json:"authors"`
				Publisher     string   `json:"publisher"`
				PublishedDate string   `json:"publishedDate"`
				ImageLinks    struct {
					SmallThumbnail string `json:"smallThumbnail"`
					Thumbnail      string `json:"thumbnail"`
				} `json:"imageLinks"`
			} `json:"volumeInfo"`
		} `json:"items"`
	}
	if err := getJSON(ctx, u, &payload); err != nil {
		return nil, err
	}
	out := make([]Candidate, 0, len(payload.Items))
	for _, it := range payload.Items {
		vi := it.VolumeInfo
		title := vi.Title
		if vi.Subtitle != "" {
			title += "：" + vi.Subtitle
		}
		cover := vi.ImageLinks.Thumbnail
		if cover == "" {
			cover = vi.ImageLinks.SmallThumbnail
		}
		if strings.HasPrefix(cover, "http://") { // 统一走 https
			cover = "https://" + strings.TrimPrefix(cover, "http://")
		}
		year := ""
		if len(vi.PublishedDate) >= 4 {
			year = vi.PublishedDate[:4]
		}
		c := Candidate{
			Source: "Google Books", Title: title,
			Author: strings.Join(vi.Authors, "、"),
			Year:   year, Publisher: vi.Publisher,
			CoverURL: cover, Detail: it.ID,
		}
		c.Score = score(q, c)
		out = append(out, c)
	}
	return out, nil
}

// ---------- Open Library ----------

type openLibrary struct{}

func (openLibrary) Name() string { return "Open Library" }

func (openLibrary) Search(ctx context.Context, q Query, limit int) ([]Candidate, error) {
	text := q.Text()
	if text == "" {
		return nil, errEmptyQuery()
	}
	if limit <= 0 || limit > 20 {
		limit = 8
	}
	u := openLibraryURL + "?q=" + url.QueryEscape(text) +
		"&limit=" + strconv.Itoa(limit) + "&fields=key,title,author_name,first_publish_year,publisher,cover_i,isbn"
	var payload struct {
		Docs []struct {
			Key            string   `json:"key"`
			Title          string   `json:"title"`
			AuthorName     []string `json:"author_name"`
			FirstPublish   int      `json:"first_publish_year"`
			Publisher      []string `json:"publisher"`
			CoverI         int      `json:"cover_i"`
			ISBN           []string `json:"isbn"`
		} `json:"docs"`
	}
	if err := getJSON(ctx, u, &payload); err != nil {
		return nil, err
	}
	out := make([]Candidate, 0, len(payload.Docs))
	for _, d := range payload.Docs {
		c := Candidate{
			Source: "Open Library", Title: d.Title,
			Author: strings.Join(d.AuthorName, "、"),
			Detail: d.Key,
		}
		if d.FirstPublish > 0 {
			c.Year = strconv.Itoa(d.FirstPublish)
		}
		if len(d.Publisher) > 0 {
			c.Publisher = d.Publisher[0]
		}
		if d.CoverI > 0 {
			c.CoverURL = "https://covers.openlibrary.org/b/id/" + strconv.Itoa(d.CoverI) + "-M.jpg"
		}
		c.Score = score(q, c)
		out = append(out, c)
	}
	return out, nil
}

// score 用「标题/作者包含关系」粗略打分，供排序与「高置信」判定。
func score(q Query, c Candidate) float64 {
	s := 0.0
	qt := strings.ToLower(strings.TrimSpace(q.Title))
	if qt == "" {
		qt = strings.ToLower(strings.TrimSpace(q.Series))
	}
	ct := strings.ToLower(c.Title)
	switch {
	case qt == "":
	case ct == qt:
		s += 0.6
	case strings.Contains(ct, qt) || strings.Contains(qt, ct):
		s += 0.45
	}
	if q.Author != "" && c.Author != "" && strings.Contains(strings.ToLower(c.Author), strings.ToLower(q.Author)) {
		s += 0.25
	}
	if q.Number != "" && strings.Contains(ct, strings.ToLower(q.Number)) {
		s += 0.15
	}
	if c.CoverURL != "" {
		s += 0.05
	}
	if s > 1 {
		s = 1
	}
	return s
}
