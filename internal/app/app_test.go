package app

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/RexKang/bookrest/internal/store"
	"github.com/RexKang/bookrest/internal/testutil"
)

// setup 造一个临时库 + 临时配置，返回已添加该库的 App。
func setup(t *testing.T) (*App, string, string) {
	t.Helper()
	base := t.TempDir()
	lib := filepath.Join(base, "lib")
	cfg := filepath.Join(base, "config.json")

	must := func(err error) {
		if err != nil {
			t.Fatal(err)
		}
	}
	must(testutil.WriteCBZ(filepath.Join(lib, "SeriesA", "第01卷.cbz"),
		testutil.CBZOpts{Pages: 5, ComicInfo: true, Series: "SeriesA", Number: "1", Title: "第一卷", Seed: 1}))
	must(testutil.WriteCBZ(filepath.Join(lib, "SeriesA", "第02卷.cbz"),
		testutil.CBZOpts{Pages: 6, ComicInfo: true, Series: "SeriesA", Number: "2", Title: "第二卷", Seed: 2}))
	must(testutil.WriteCBZ(filepath.Join(lib, "SeriesA", "第04卷.cbz"),
		testutil.CBZOpts{Pages: 7, ComicInfo: true, Series: "SeriesA", Number: "4", Title: "第四卷", Seed: 3}))
	must(testutil.WriteCBZ(filepath.Join(lib, "SeriesB", "无编号.cbz"),
		testutil.CBZOpts{Pages: 3, Seed: 4}))

	a, err := New(cfg, nil)
	must(err)
	// 镜像目录隔离到临时目录：测试不得污染真实 %APPDATA%/bookrest
	s0 := a.Settings()
	s0.MirrorDir = filepath.Join(base, "mirror")
	must(a.SaveSettings(s0))
	if err := a.loadLibraryMirror(); err != nil {
		t.Fatal(err)
	}
	if _, err := a.AddLibrary(lib); err != nil {
		t.Fatal(err)
	}
	// 默认关闭库内回写：多数用例只验证本机行为，避免异步写库干扰临时目录清理
	s := a.Settings()
	s.WriteBack = false
	must(a.SaveSettings(s))
	return a, lib, cfg
}

// TestWriteBackCreatesSnapshot 覆盖「意图异步回写库内 .bookrest/」这条路径。
func TestWriteBackCreatesSnapshot(t *testing.T) {
	a, lib, _ := setup(t)
	if _, err := a.Scan(); err != nil {
		t.Fatal(err)
	}
	s := a.Settings()
	s.WriteBack = true
	if err := a.SaveSettings(s); err != nil {
		t.Fatal(err)
	}
	id := a.Items()[0].ID
	if err := a.SetOverride(id, store.Override{Tags: []string{"回写测试"}}); err != nil {
		t.Fatal(err)
	}
	snap := filepath.Join(lib, ".bookrest", "shelf.json")
	deadline := 40
	for i := 0; i < deadline; i++ {
		if _, err := os.Stat(snap); err == nil {
			data, _ := os.ReadFile(snap)
			if len(data) > 0 && stringContains(string(data), "回写测试") {
				return // 快照已落库
			}
		}
		sleepMS(50)
	}
	t.Fatalf("库内快照未在预期时间内生成：%s", snap)
}

func stringContains(s, sub string) bool { return len(s) >= len(sub) && (func() bool { return indexOf(s, sub) >= 0 })() }
func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
func sleepMS(ms int) { time.Sleep(time.Duration(ms) * time.Millisecond) }

func TestScanItemsAndStatus(t *testing.T) {
	a, lib, _ := setup(t)

	if st := a.Status(); !st.Online || st.Root != lib {
		t.Fatalf("库状态异常: %+v", st)
	}
	sum, err := a.Scan()
	if err != nil {
		t.Fatal(err)
	}
	if sum.Total != 4 || sum.Added != 4 {
		t.Fatalf("扫描结果异常: %+v", sum)
	}
	items := a.Items()
	if len(items) != 4 {
		t.Fatalf("应有 4 条，得到 %d", len(items))
	}
	// 第二卷应带内嵌元数据
	var found bool
	for _, it := range items {
		if it.Number == "1" && it.Series == "SeriesA" && it.Pages == 5 {
			found = true
		}
	}
	if !found {
		t.Fatalf("未找到预期条目: %+v", items)
	}
	// 重复扫描不应重复计数
	sum2, err := a.Scan()
	if err != nil {
		t.Fatal(err)
	}
	if sum2.Added != 0 || sum2.Total != 4 {
		t.Fatalf("二次扫描异常: %+v", sum2)
	}
}

func TestShelfOpsAndOverrides(t *testing.T) {
	a, _, _ := setup(t)
	if _, err := a.Scan(); err != nil {
		t.Fatal(err)
	}
	items := a.Items()
	id := items[0].ID

	sh, err := a.CreateShelf("必读")
	if err != nil {
		t.Fatal(err)
	}
	if err := a.MoveItem(id, sh.ID, 0); err != nil {
		t.Fatal(err)
	}
	// 再放一本到前面，验证顺序
	id2 := items[1].ID
	if err := a.MoveItem(id2, sh.ID, 0); err != nil {
		t.Fatal(err)
	}
	shelves := a.Shelves()
	if len(shelves) != 1 || len(shelves[0].Order) != 2 || shelves[0].Order[0] != id2 {
		t.Fatalf("书架顺序异常: %+v", shelves)
	}
	// 重新载入（模拟重启）后顺序应保持
	a2, err := New(a.cfgFn, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := a2.Shelves(); len(got) != 1 || got[0].Order[0] != id2 {
		t.Fatalf("重载后书架顺序丢失: %+v", got)
	}
	// 覆盖项：标题/标签/评分
	if err := a.SetOverride(id, store.Override{Title: "自定义标题", Tags: []string{"画集"}, Rating: 5}); err != nil {
		t.Fatal(err)
	}
	items = a.Items()
	for _, it := range items {
		if it.ID == id {
			if it.Title != "自定义标题" || it.Rating != 5 || len(it.Tags) != 1 {
				t.Fatalf("覆盖项未生效: %+v", it)
			}
		}
	}
	// 移出书架
	if err := a.MoveItem(id, "", -1); err != nil {
		t.Fatal(err)
	}
	for _, it := range a.Items() {
		if it.ID == id && it.ShelfID != "" {
			t.Fatalf("移出书架失败: %+v", it)
		}
	}
}

func TestOfflineIsReadOnly(t *testing.T) {
	a, lib, _ := setup(t)
	if _, err := a.Scan(); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(lib); err != nil {
		t.Fatal(err)
	}
	st := a.Status()
	if st.Online {
		t.Fatal("目录已删除，应判为离线")
	}
	if st.Hint == "" {
		t.Fatal("离线时应给出提示文案")
	}
	// 关键回归：离线时 Scan 必须直接返回错误，不能自锁死锁
	if _, err := a.Scan(); err == nil {
		t.Fatal("离线扫描应返回错误")
	}
	// 离线时书架仍可读
	if len(a.Items()) != 4 {
		t.Fatalf("离线时条目应仍可读，得到 %d", len(a.Items()))
	}
}

func TestReportFindsGapAndCorrupt(t *testing.T) {
	a, lib, _ := setup(t)
	// 造一个损坏文件与无封面文件
	if err := os.WriteFile(filepath.Join(lib, "SeriesB", "坏文件.cbz"), []byte("not a zip"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := testutil.WriteCBZ(filepath.Join(lib, "SeriesB", "无封面 第1卷.cbz"), testutil.CBZOpts{NoCover: true, ComicInfo: true, Series: "SeriesB", Number: "1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Scan(); err != nil {
		t.Fatal(err)
	}
	rep := a.Report()
	if rep.Total < 6 {
		t.Fatalf("报告条目数异常: %d", rep.Total)
	}
	var gaps, corrupts, nocovers int
	for _, f := range rep.Findings {
		switch f.Kind {
		case "gap":
			gaps++
			if f.Series == "SeriesA" && len(f.Missing) == 1 && f.Missing[0] == 3 {
				// 1、2、4 → 缺 3 ✓
			} else {
				t.Fatalf("缺卷识别异常: %+v", f)
			}
		case "corrupt":
			corrupts += len(f.Items)
		case "nocover":
			nocovers += len(f.Items)
		}
	}
	if gaps != 1 || corrupts != 1 || nocovers != 1 {
		t.Fatalf("体检结果异常: gaps=%d corrupt=%d nocover=%d", gaps, corrupts, nocovers)
	}
}

func TestSettingsAndThumb(t *testing.T) {
	a, _, _ := setup(t)
	if _, err := a.Scan(); err != nil {
		t.Fatal(err)
	}
	s := a.Settings()
	s.Theme = "light"
	if err := a.SaveSettings(s); err != nil {
		t.Fatal(err)
	}
	if got := a.Settings().Theme; got != "light" {
		t.Fatalf("设置未保存: %q", got)
	}
	// 缩略图：应能生成 JPEG 且第二次命中缓存
	id := a.Items()[0].ID
	data, err := a.ThumbBytes(id)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) < 3 || data[0] != 0xFF || data[1] != 0xD8 || data[2] != 0xFF {
		t.Fatalf("缩略图不是 JPEG: % x", data[:3])
	}
	data2, err := a.ThumbBytes(id)
	if err != nil || len(data2) != len(data) {
		t.Fatalf("缓存未命中: %v", err)
	}
	// 颜色计算
	colors := a.SpineColors()
	if len(colors) == 0 {
		t.Fatal("应计算封面主色")
	}
}


