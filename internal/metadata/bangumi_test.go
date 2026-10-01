package metadata

import (
	"context"
	"net/http"
	"testing"
)

func TestBangumiParsing(t *testing.T) {
	body := `{"data":[
	  {"id":12,"name":"ONE PIECE","name_cn":"海贼王","date":"1997-12-24",
	   "images":{"large":"http://lain.bgm.tv/pic/cover/l/1.jpg","common":"https://lain.bgm.tv/pic/cover/c/1.jpg"},
	   "infobox":[{"key":"册数","value":"105"},{"key":"出版社","value":"集英社"},{"key":"作者","value":["尾田荣一郎"]}]}
	]}`
	srv := withTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("Bangumi 搜索应为 POST，得到 %s", r.Method)
		}
		if ct := r.Header.Get("Content-Type"); ct != "application/json" {
			t.Errorf("Content-Type 应为 json，得到 %s", ct)
		}
		_, _ = w.Write([]byte(body))
	})
	old := bangumiURL
	bangumiURL = srv.URL
	t.Cleanup(func() { bangumiURL = old })

	got, err := (bangumi{}).Search(context.Background(), Query{Series: "海贼王"}, 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("应解析出 1 条，得到 %d", len(got))
	}
	c := got[0]
	if c.Title != "海贼王" || c.Year != "1997" || c.Number != "105" || c.Author != "尾田荣一郎" {
		t.Fatalf("字段解析异常：%+v", c)
	}
	if c.CoverURL != "https://lain.bgm.tv/pic/cover/l/1.jpg" {
		t.Fatalf("封面应为 https 且取 large：%s", c.CoverURL)
	}
}

func TestNormalizeProxyServer(t *testing.T) {
	cases := map[string]string{
		"127.0.0.1:10808":                        "http://127.0.0.1:10808",
		"http=127.0.0.1:1;https=127.0.0.1:2":     "http://127.0.0.1:2",
		"http=127.0.0.1:1":                       "http://127.0.0.1:1",
		"":                                       "",
		"  ":                                     "",
		"http://127.0.0.1:7890":                  "http://127.0.0.1:7890",
	}
	for in, want := range cases {
		if got := normalizeProxyServer(in); got != want {
			t.Fatalf("normalizeProxyServer(%q) = %q，期望 %q", in, got, want)
		}
	}
}
