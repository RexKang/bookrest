package domain

import (
	"os"
	"path/filepath"
	"testing"
)

func writeFile(t *testing.T, dir, name string, data []byte) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// TC-D-01：同一文件两次指纹一致。
func TestFingerprintStable(t *testing.T) {
	dir := t.TempDir()
	data := make([]byte, 100*1024)
	for i := range data {
		data[i] = byte(i * 7)
	}
	p := writeFile(t, dir, "a.cbz", data)

	fp1, err := FingerprintFile(p)
	if err != nil {
		t.Fatal(err)
	}
	fp2, err := FingerprintFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if fp1.ID() != fp2.ID() {
		t.Fatalf("同文件两次指纹不一致: %s vs %s", fp1.ID(), fp2.ID())
	}
	if len(fp1.ID()) != 16 {
		t.Fatalf("ID 长度应为 16，得到 %d", len(fp1.ID()))
	}
}

// TC-D-02：首 64KB 相同但大小不同 → 身份不同。
func TestFingerprintSizeSensitive(t *testing.T) {
	dir := t.TempDir()
	head := make([]byte, 70*1024)
	for i := range head {
		head[i] = byte(i % 251)
	}
	p1 := writeFile(t, dir, "small.cbz", head)
	p2 := writeFile(t, dir, "big.cbz", append(append([]byte{}, head...), []byte("tail-bytes")...))

	f1, _ := FingerprintFile(p1)
	f2, _ := FingerprintFile(p2)
	if f1.ID() == f2.ID() {
		t.Fatal("首 64KB 相同但大小不同的两个文件不应同 ID")
	}
}

// TC-D-03：碰撞降级身份与稳定身份不同，且随路径变化。
func TestUnstableID(t *testing.T) {
	a := UnstableID("dir/a.cbz", 100, 1000)
	b := UnstableID("dir/b.cbz", 100, 1000)
	if a == b {
		t.Fatal("不同路径的降级身份不应相同")
	}
	if a == UnstableID("dir/a.cbz", 100, 1001) {
		t.Fatal("mtime 变化应改变降级身份")
	}
}

// TC-D-04：三者齐备时取用户覆盖。
func TestMergeUserWins(t *testing.T) {
	user := Meta{Title: "我的标题", Src: SrcUser}
	embedded := Meta{Title: "内嵌标题", Series: "内嵌系列", Author: "内嵌作者", Src: SrcComicInfo}
	filename := Meta{Series: "文件名系列", Number: "3", Src: SrcFilename}

	got := Merge(user, embedded, filename)
	if got.Title != "我的标题" {
		t.Errorf("Title 应取用户覆盖，得到 %q", got.Title)
	}
	if got.Series != "内嵌系列" {
		t.Errorf("Series 应取内嵌，得到 %q", got.Series)
	}
	if got.Number != "3" {
		t.Errorf("Number 应回退到文件名，得到 %q", got.Number)
	}
	if got.Src != SrcUser {
		t.Errorf("Src 应为 user，得到 %q", got.Src)
	}
}

// TC-D-05：只有文件名可用时走文件名。
func TestMergeFilenameOnly(t *testing.T) {
	got := Merge(Meta{Src: SrcUser}, Meta{Src: SrcComicInfo}, ParseFilename("[组] 某系列 第02卷.cbz"))
	if got.Series != "某系列" || got.Number != "2" {
		t.Fatalf("文件名解析结果未生效: %+v", got)
	}
	if got.Src != SrcFilename {
		t.Fatalf("Src 应为 filename，得到 %q", got.Src)
	}
}
