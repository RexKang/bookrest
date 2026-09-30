package server

import (
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/RexKang/bookrest/internal/app"
)

func newApp(t *testing.T) *app.App {
	t.Helper()
	a, err := app.New(filepath.Join(t.TempDir(), "config.json"), nil)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

// newReq 造一个 Host 合法的请求（httptest 默认 Host=example.com，会被 Host 守卫拦下）。
func newReq(method, target string, body io.Reader) *http.Request {
	r := httptest.NewRequest(method, target, body)
	r.Host = "127.0.0.1:8788"
	return r
}

func testFS() fstest.MapFS {
	return fstest.MapFS{
		"index.html": &fstest.MapFile{Data: []byte("<html>拾书 Bookrest</html>")},
		"app.js":     &fstest.MapFile{Data: []byte("/* app */")},
	}
}

// TestRejectNonLoopbackWithoutAuthorization 是核心安全约束：
// 未经用户显式授权，绝不把书架绑定到非本机地址。
func TestRejectNonLoopbackWithoutAuthorization(t *testing.T) {
	a := newApp(t)
	for _, addr := range []string{"0.0.0.0:8788", ":8788", "192.168.1.10:8788", "[::]:8788"} {
		if _, _, err := Handler(a, Options{Addr: addr, Quiet: true}); err == nil {
			t.Fatalf("%s 属于对外暴露，应被拒绝启动", addr)
		} else if !strings.Contains(err.Error(), "allow-remote") {
			t.Fatalf("拒绝信息应提示如何显式授权，得到：%v", err)
		}
	}
	// 显式授权后允许绑定
	_, opts, err := Handler(a, Options{Addr: "0.0.0.0:8788", AllowRemote: true, Quiet: true})
	if err != nil {
		t.Fatalf("显式授权后应可绑定：%v", err)
	}
	if opts.Token == "" {
		t.Fatal("应自动生成访问令牌")
	}
	// 回环地址默认可绑定
	for _, addr := range []string{"127.0.0.1:8788", "localhost:8788", "[::1]:8788"} {
		if _, _, err := Handler(a, Options{Addr: addr, Quiet: true}); err != nil {
			t.Fatalf("回环地址 %s 应可绑定：%v", addr, err)
		}
	}
	// 地址格式错误也要拦住
	if _, _, err := Handler(a, Options{Addr: "127.0.0.1", Quiet: true}); err == nil {
		t.Fatal("缺少端口的地址应被拒绝")
	}
}

// TestTokenRequiredEverywhere：没有令牌什么也拿不到（含静态资源与 API）。
func TestTokenRequiredEverywhere(t *testing.T) {
	a := newApp(t)
	h, opts, err := Handler(a, Options{Addr: "127.0.0.1:8788", Dist: testFS(), Quiet: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"/", "/app.js", "/api/items", "/api/settings", "/thumb?id=x"} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, newReq("GET", p, nil))
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("%s 无令牌应返回 401，得到 %d", p, rec.Code)
		}
	}
	// 错误令牌同样 401
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, newReq("GET", "/api/items?token=deadbeef", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("错误令牌应返回 401，得到 %d", rec.Code)
	}
	// 正确令牌：种下 HttpOnly + SameSite=Strict 的 Cookie 并跳转
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, newReq("GET", "/?token="+opts.Token, nil))
	if rec.Code != http.StatusFound {
		t.Fatalf("带令牌访问首页应 302 去掉地址栏令牌，得到 %d", rec.Code)
	}
	cookies := rec.Result().Cookies()
	if len(cookies) == 0 || cookies[0].Name != cookieName || cookies[0].Value != opts.Token {
		t.Fatalf("应种下令牌 Cookie，得到 %+v", cookies)
	}
	if !cookies[0].HttpOnly || cookies[0].SameSite != http.SameSiteStrictMode {
		t.Fatalf("Cookie 应为 HttpOnly + SameSite=Strict，得到 %+v", cookies[0])
	}
	// 带 Cookie 后可正常读取（API + 静态资源）
	for _, p := range []string{"/", "/api/items", "/api/settings", "/api/version"} {
		req := newReq("GET", p, nil)
		req.AddCookie(cookies[0])
		rec2 := httptest.NewRecorder()
		h.ServeHTTP(rec2, req)
		if rec2.Code != http.StatusOK {
			t.Fatalf("%s 带 Cookie 应 200，得到 %d（%s）", p, rec2.Code, rec2.Body.String())
		}
	}
	// 带 Cookie 的写操作可用
	req := newReq("POST", "/api/shelf/create", strings.NewReader(`{"name":"安全测试"}`))
	req.AddCookie(cookies[0])
	rec3 := httptest.NewRecorder()
	h.ServeHTTP(rec3, req)
	if rec3.Code != http.StatusOK {
		t.Fatalf("带 Cookie 的写操作应成功，得到 %d", rec3.Code)
	}
}

// TestNoLibraryDoesNotCrash：还没添加库时，所有查询接口都必须正常返回（不能崩）。
// 崩一次 = B/S 模式下服务停摆，属于可被远程触发的拒绝服务。
func TestNoLibraryDoesNotCrash(t *testing.T) {
	a := newApp(t) // 没有添加任何库
	h, opts, err := Handler(a, Options{Addr: "127.0.0.1:8788", Dist: testFS(), Quiet: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"/api/items", "/api/status", "/api/shelves", "/api/report", "/api/colors", "/api/libraries", "/api/drives"} {
		req := newReq("GET", p, nil)
		req.AddCookie(&http.Cookie{Name: cookieName, Value: opts.Token})
		rec := httptest.NewRecorder()
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("%s 触发 panic：%v", p, r)
				}
			}()
			h.ServeHTTP(rec, req)
		}()
		if rec.Code != http.StatusOK {
			t.Fatalf("%s 应返回 200，得到 %d", p, rec.Code)
		}
	}
}

// TestHostHeaderGuard：只认本机 Host，阻断 DNS rebinding。
func TestHostHeaderGuard(t *testing.T) {
	a := newApp(t)
	h, opts, err := Handler(a, Options{Addr: "127.0.0.1:8788", Dist: testFS(), Quiet: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, host := range []string{"evil.com", "bookrest.attacker.net:8788", "192.168.1.10:8788"} {
		req := newReq("GET", "/api/items", nil)
		req.Host = host
		req.AddCookie(&http.Cookie{Name: cookieName, Value: opts.Token})
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("Host=%s 应被拒绝（403），得到 %d", host, rec.Code)
		}
	}
	for _, host := range []string{"127.0.0.1:8788", "localhost:8788", "127.0.0.1"} {
		req := newReq("GET", "/api/items", nil)
		req.Host = host
		req.AddCookie(&http.Cookie{Name: cookieName, Value: opts.Token})
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("Host=%s 应被接受，得到 %d", host, rec.Code)
		}
	}
}

// TestNoCORSHeaders：不发送任何跨域许可头，配合 SameSite=Strict 阻断 CSRF。
func TestNoCORSHeaders(t *testing.T) {
	a := newApp(t)
	h, opts, err := Handler(a, Options{Addr: "127.0.0.1:8788", Dist: testFS(), Quiet: true})
	if err != nil {
		t.Fatal(err)
	}
	req := newReq("GET", "/api/items", nil)
	req.Header.Set("Origin", "https://evil.com")
	req.AddCookie(&http.Cookie{Name: cookieName, Value: opts.Token})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	for k := range rec.Header() {
		if strings.HasPrefix(strings.ToLower(k), "access-control-") {
			t.Fatalf("不应出现 CORS 头：%s", k)
		}
	}
	if rec.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatal("应带 nosniff")
	}
}

// TestTokensAreRandom：令牌每次生成都不同。
func TestTokensAreRandom(t *testing.T) {
	a := newApp(t)
	seen := map[string]bool{}
	for i := 0; i < 8; i++ {
		_, opts, err := Handler(a, Options{Addr: "127.0.0.1:8788", Quiet: true})
		if err != nil {
			t.Fatal(err)
		}
		if len(opts.Token) < 32 {
			t.Fatalf("令牌应至少 128 位随机，得到 %q", opts.Token)
		}
		if seen[opts.Token] {
			t.Fatal("令牌重复出现")
		}
		seen[opts.Token] = true
	}
}
