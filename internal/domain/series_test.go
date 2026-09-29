package domain

import (
	"reflect"
	"testing"
)

// TC-D-06：命名变体解析（目标正确率 ≥ 90%，此处覆盖 8 种常见写法）。
func TestParseFilenameVariants(t *testing.T) {
	cases := []struct {
		in     string
		series string
		number string
	}{
		{"[汉化组] 海贼王 第01卷.cbz", "海贼王", "1"},
		{"葬送的芙莉莲 v03.cbz", "葬送的芙莉莲", "3"},
		{"Berserk Vol. 12.cbz", "Berserk", "12"},
		{"One Piece #001.cbz", "One Piece", "1"},
		{"Series Name - 07 (2020).cbz", "Series Name", "7"},
		{"チェンソーマン 第3話.cbz", "チェンソーマン", "3"},
		{"漫画名 12.cbz", "漫画名", "12"},
		{"无编号的书名.cbz", "无编号的书名", ""},
	}
	for _, c := range cases {
		got := ParseFilename(c.in)
		if got.Series != c.series || got.Number != c.number {
			t.Errorf("ParseFilename(%q) = series %q number %q，期望 %q / %q",
				c.in, got.Series, got.Number, c.series, c.number)
		}
	}
}

// TC-D-07 / TC-D-08：断档识别与不误报。
func TestFindGaps(t *testing.T) {
	if got := FindGaps([]float64{1, 2, 3, 4, 5, 6, 7, 8, 10}); !reflect.DeepEqual(got, []int{9}) {
		t.Errorf("应报缺 9，得到 %v", got)
	}
	if got := FindGaps([]float64{1, 2, 3}); got != nil {
		t.Errorf("连续系列不应报缺，得到 %v", got)
	}
	if got := FindGaps([]float64{1, 3, 5}); !reflect.DeepEqual(got, []int{2, 4}) {
		t.Errorf("应报缺 2、4，得到 %v", got)
	}
	if got := FindGaps(nil); got != nil {
		t.Errorf("空集合不应报缺，得到 %v", got)
	}
	// 非整数卷号不参与断档判断（避免 1.5 卷被当成缺口）
	if got := FindGaps([]float64{1, 1.5, 2}); got != nil {
		t.Errorf("含非整数卷号时不应报缺，得到 %v", got)
	}
}
