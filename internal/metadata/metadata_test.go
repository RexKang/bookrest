package metadata

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func withTestServer(t *testing.T, handler http.HandlerFunc) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	// 测试服务器跑在 http://127.0.0.1（生产要求 https + 白名单域名），测试期放行
	testAllowInsecure = true
	t.Cleanup(func() { testAllowInsecure = false })
	return srv
}

func TestGoogleBooksParsing(t *testing.T) {
	body := `{"items":[
	  {"id":"abc","volumeInfo":{"title":"股息不说谎","authors":["Charles B. Carlson"],
	   "publishedDate":"2011-05-01","publisher":"某某出版社",
	   "imageLinks":{"thumbnail":"http://books.google.com/books/content?id=abc&printsec=frontcover"}}},
	  {"id":"def","volumeInfo":{"title":"无关的书","authors":["别人"]}}
	]}`
	srv := withTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.RawQuery, "q=") {
			t.Errorf("查询串缺失：%s", r.URL.RawQuery)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	})
	old := googleBooksURL
	googleBooksURL = srv.URL
	t.Cleanup(func() { googleBooksURL = old })

	q := Query{Title: "股息不说谎", Author: "Carlson"}
	got, err := (googleBooks{}).Search(context.Background(), q, 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("应解析出 2 条，得到 %d", len(got))
	}
	first := got[0]
	if first.Title != "股息不说谎" || first.Author != "Charles B. Carlson" || first.Year != "2011" {
		t.Fatalf("字段解析异常：%+v", first)
	}
	// http 封面应被提升成 https（防明文）
	if !strings.HasPrefix(first.CoverURL, "https://") {
		t.Fatalf("封面 URL 应为 https：%s", first.CoverURL)
	}
	// 贴合度：完全匹配的应高于无关的
	if first.Score <= got[1].Score {
		t.Fatalf("打分排序异常：%v vs %v", first.Score, got[1].Score)
	}
}

func TestOpenLibraryParsing(t *testing.T) {
	body := `{"docs":[
	  {"key":"/works/OL1W","title":"从冲浪到潜水","author_name":["散户乙"],
	   "first_publish_year":2020,"publisher":["某某社"],"cover_i":12345}
	]}`
	srv := withTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(body))
	})
	old := openLibraryURL
	openLibraryURL = srv.URL
	t.Cleanup(func() { openLibraryURL = old })

	got, err := (openLibrary{}).Search(context.Background(), Query{Title: "从冲浪到潜水"}, 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("应解析出 1 条，得到 %d", len(got))
	}
	c := got[0]
	if c.Title != "从冲浪到潜水" || c.Year != "2020" || c.Author != "散户乙" {
		t.Fatalf("字段解析异常：%+v", c)
	}
	if c.CoverURL != "https://covers.openlibrary.org/b/id/12345-M.jpg" {
		t.Fatalf("封面 URL 拼装异常：%s", c.CoverURL)
	}
}

// TestHostAllowlist 是安全红线：非白名单域名一律拒绝（防 SSRF）。
func TestHostAllowlist(t *testing.T) {
	bad := []string{
		"http://www.googleapis.com/books/v1/volumes", // 非 https
		"https://evil.com/x.jpg",
		"https://127.0.0.1:8080/x.jpg",
		"https://covers.openlibrary.org.evil.com/x.jpg",
		"file:///c:/windows/win.ini",
		"",
	}
	for _, u := range bad {
		if HostAllowed(u) {
			t.Fatalf("应拒绝：%s", u)
		}
	}
	good := []string{
		"https://www.googleapis.com/books/v1/volumes?q=x",
		"https://covers.openlibrary.org/b/id/1-M.jpg",
		"https://openlibrary.org/search.json?q=x",
	}
	for _, u := range good {
		if !HostAllowed(u) {
			t.Fatalf("应放行：%s", u)
		}
	}
}

func TestQueryText(t *testing.T) {
	cases := []struct {
		q    Query
		want string
	}{
		{Query{Title: "股息不说谎", Author: "Carlson"}, "股息不说谎 Carlson"},
		{Query{Series: "海贼王", Number: "12"}, "海贼王 12"},
		{Query{Title: "书"}, "书"},
	}
	for _, c := range cases {
		if got := c.q.Text(); got != c.want {
			t.Fatalf("Query.Text() = %q，期望 %q", got, c.want)
		}
	}
	if (Query{}).Text() != "" {
		t.Fatal("空查询应返回空串")
	}
}

func TestSourcesSelection(t *testing.T) {
	if len(Sources(nil)) != len(AvailableSourceNames()) {
		t.Fatalf("默认应返回全部源，得到 %d", len(Sources(nil)))
	}
	one := Sources([]string{"open library"})
	if len(one) != 1 || one[0].Name() != "Open Library" {
		t.Fatalf("按名筛选异常：%+v", one)
	}
	if len(Sources([]string{"不存在的源"})) != 0 {
		t.Fatal("未知源应返回空")
	}
}
