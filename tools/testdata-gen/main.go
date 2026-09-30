// Command testdata-gen 生成分级测试数据（l1/l2/l3/l4/edge）。
//
// 硬约束：累计写入不得超过 --max-bytes（默认 900MB），超限立即中止并返回非 0。
// 幂等：同一目录 + 同一规模 + 同一种子，若 manifest 校验通过则直接复用，不重建。
//
// 用法：
//
//	testdata-gen --scale l1 --out D:/bookrest-testlib/l1
//	testdata-gen --scale l1 --out ... --dry-run      # 只算预算不写盘
//	testdata-gen --clean --out ...
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/RexKang/bookrest/internal/testutil"
)

const manifestVersion = 1

type manifest struct {
	Scale       string `json:"scale"`
	Seed        int64  `json:"seed"`
	Books       int    `json:"books"`
	Files       int    `json:"files"`
	Bytes       int64  `json:"bytes"`
	Version     int    `json:"version"`
	GeneratedAt string `json:"generated_at"`
}

type budget struct {
	limit   int64
	written int64
}

func (b *budget) add(n int64) error {
	b.written += n
	if b.written > b.limit {
		return fmt.Errorf("测试数据写入超限：已写 %d 字节 > 上限 %d 字节（硬约束 1GB）", b.written, b.limit)
	}
	return nil
}

type scaleSpec struct {
	books    int
	pages    int
	pageW    int
	pageH    int
	series   int
	comicInf bool
}

var specs = map[string]scaleSpec{
	"l1":   {books: 300, pages: 6, pageW: 96, pageH: 144, series: 20, comicInf: true},
	"l2":   {books: 2000, pages: 6, pageW: 96, pageH: 144, series: 60, comicInf: true},
	"l3":   {books: 20000, pages: 5, pageW: 80, pageH: 120, series: 400, comicInf: true},
	"l4":   {books: 60, pages: 8, pageW: 1600, pageH: 2400, series: 6, comicInf: true},
	"edge": {books: 0, series: 0}, // 由 buildEdge 生成
}

func main() {
	scale := flag.String("scale", "l1", "规模：l1|l2|l3|l4|edge")
	out := flag.String("out", "", "输出目录")
	seed := flag.Int64("seed", 42, "随机种子（同种子=同数据）")
	maxBytes := flag.Int64("max-bytes", 900*1024*1024, "写入硬上限（字节）")
	dryRun := flag.Bool("dry-run", false, "只打印预算，不写盘")
	clean := flag.Bool("clean", false, "删除输出目录后退出")
	flag.Parse()

	if *out == "" {
		fmt.Fprintln(os.Stderr, "必须指定 --out")
		os.Exit(2)
	}
	if *clean {
		if err := os.RemoveAll(*out); err != nil {
			fmt.Fprintln(os.Stderr, "清理失败:", err)
			os.Exit(1)
		}
		fmt.Println("已清理:", *out)
		return
	}
	spec, ok := specs[*scale]
	if !ok {
		fmt.Fprintln(os.Stderr, "未知规模:", *scale)
		os.Exit(2)
	}

	// manifest 写在库目录之外：库内只应有书，扫描器也只认书籍格式
	mfPath := *out + ".manifest.json"
	if mf, err := readManifest(mfPath); err == nil && mf.Scale == *scale && mf.Seed == *seed && mf.Version == manifestVersion {
		fmt.Printf("复用已有数据（scale=%s seed=%d books=%d bytes=%.1fMB）\n", mf.Scale, mf.Seed, mf.Books, float64(mf.Bytes)/1048576)
		return
	}
	if *dryRun {
		est := estimate(spec)
		fmt.Printf("[dry-run] scale=%s 预计 %d 本、约 %.1fMB（上限 %.0fMB）\n", *scale, spec.books, float64(est)/1048576, float64(*maxBytes)/1048576)
		return
	}

	if err := os.MkdirAll(*out, 0o755); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	b := &budget{limit: *maxBytes}
	start := time.Now()

	var mf manifest
	var err error
	if *scale == "edge" {
		mf, err = buildEdge(*out, *seed, b)
	} else {
		mf, err = buildScale(*out, *scale, spec, *seed, b)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "生成失败:", err)
		os.Exit(1)
	}
	mf.Scale, mf.Seed, mf.Version, mf.GeneratedAt = *scale, *seed, manifestVersion, time.Now().Format(time.RFC3339)
	data, _ := json.MarshalIndent(mf, "", "  ")
	if err := os.WriteFile(mfPath, data, 0o644); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Printf("完成 scale=%s：%d 本 / %d 文件 / %.1fMB，用时 %s\n",
		*scale, mf.Books, mf.Files, float64(mf.Bytes)/1048576, time.Since(start).Round(time.Millisecond))
	fmt.Printf("预算：已写 %.1fMB / 上限 %.0fMB\n", float64(b.written)/1048576, float64(*maxBytes)/1048576)
}

func readManifest(path string) (manifest, error) {
	var mf manifest
	data, err := os.ReadFile(path)
	if err != nil {
		return mf, err
	}
	err = json.Unmarshal(data, &mf)
	return mf, err
}

func estimate(s scaleSpec) int64 {
	perBook := int64(s.pages)*int64(s.pageW*s.pageH/12+1200) + 512
	return perBook * int64(s.books)
}

func buildScale(root, scale string, spec scaleSpec, seed int64, b *budget) (manifest, error) {
	mf := manifest{}
	for i := 0; i < spec.books; i++ {
		s := i % spec.series
		num := i/spec.series + 1
		dir := filepath.Join(root, fmt.Sprintf("Series%03d", s))
		name := fmt.Sprintf("Series%03d 第%02d卷.cbz", s, num)
		p := filepath.Join(dir, name)
		opts := testutil.CBZOpts{
			Pages: spec.pages, PageW: spec.pageW, PageH: spec.pageH,
			ComicInfo: spec.comicInf, Series: fmt.Sprintf("Series%03d", s),
			Number: fmt.Sprint(num), Title: name, Seed: seed + int64(i),
		}
		if err := writeAndCount(p, opts, b); err != nil {
			return mf, err
		}
		mf.Books++
		mf.Files++
		// 每 10 本夹一本 EPUB，让格式分布更真实
		if i%10 == 0 {
			ep := filepath.Join(root, "Epubs", fmt.Sprintf("book%04d.epub", i))
			if err := writeEPUBAndCount(ep, seed+int64(i), b); err != nil {
				return mf, err
			}
			mf.Books++
			mf.Files++
		}
	}
	mf.Bytes = b.written
	return mf, nil
}

// buildEdge 生成边界族：每族都对应测试方案 §2.2 的一行。
func buildEdge(root string, seed int64, b *budget) (manifest, error) {
	mf := manifest{}
	add := func(p string, opts testutil.CBZOpts) error {
		if err := writeAndCount(p, opts, b); err != nil {
			return err
		}
		mf.Books++
		mf.Files++
		return nil
	}
	// 1) 系列断档：1–8、10（缺 3、7 之外还有 9 之外的 10 → 报缺 9）
	for _, n := range []int{1, 2, 4, 5, 6, 8, 10} {
		if err := add(filepath.Join(root, "SeriesGap", fmt.Sprintf("SeriesGap 第%02d卷.cbz", n)),
			testutil.CBZOpts{Pages: 4, ComicInfo: true, Series: "SeriesGap", Number: fmt.Sprint(n), Seed: seed + int64(n)}); err != nil {
			return mf, err
		}
	}
	// 2) 封面缺失
	if err := add(filepath.Join(root, "NoCover", "无封面 第1卷.cbz"), testutil.CBZOpts{NoCover: true, ComicInfo: true, Series: "无封面", Number: "1"}); err != nil {
		return mf, err
	}
	// 3) 无内嵌元数据（走文件名解析）
	if err := add(filepath.Join(root, "NoMeta", "[汉化组] 某系列 第03卷 [扫图].cbz"), testutil.CBZOpts{Pages: 3, Seed: seed + 3}); err != nil {
		return mf, err
	}
	// 4) 精确重复（同种子同参数 → 字节完全相同）
	for _, name := range []string{"Dup/副本A.cbz", "Dup/副本B.cbz"} {
		if err := add(filepath.Join(root, filepath.FromSlash(name)), testutil.CBZOpts{Pages: 4, ComicInfo: true, Series: "Dup", Number: "1", Seed: seed + 99}); err != nil {
			return mf, err
		}
	}
	// 5) 近似重复（同封面不同页数）
	if err := add(filepath.Join(root, "NearDup", "近似A.cbz"), testutil.CBZOpts{Pages: 4, ComicInfo: true, Series: "Near", Number: "1", Seed: seed + 77}); err != nil {
		return mf, err
	}
	if err := add(filepath.Join(root, "NearDup", "近似B.cbz"), testutil.CBZOpts{Pages: 9, ComicInfo: true, Series: "Near", Number: "1", Seed: seed + 77}); err != nil {
		return mf, err
	}
	// 6) 损坏族
	if err := add(filepath.Join(root, "Corrupt", "截断.cbz"), testutil.CBZOpts{Pages: 6, Corrupt: true, Seed: seed + 5}); err != nil {
		return mf, err
	}
	zero := filepath.Join(root, "Corrupt", "空文件.cbz")
	if err := os.WriteFile(zero, nil, 0o644); err != nil {
		return mf, err
	}
	mf.Files++
	if err := os.WriteFile(filepath.Join(root, "Corrupt", "伪装扩展名.cbz"), []byte("其实是个文本文件"), 0o644); err != nil {
		return mf, err
	}
	mf.Files++
	// 7) 命名地狱
	long := strings.Repeat("超长书名", 60) + ".cbz"
	if err := add(filepath.Join(root, "Naming", long), testutil.CBZOpts{Pages: 2, Seed: seed + 6}); err != nil {
		return mf, err
	}
	if err := add(filepath.Join(root, "Naming", "📚 表情与空格 第1卷 .cbz"), testutil.CBZOpts{Pages: 2, Seed: seed + 7}); err != nil {
		return mf, err
	}
	if err := add(filepath.Join(root, "Naming", "括号【冲突】{测试}(2) 第1卷.cbz"), testutil.CBZOpts{Pages: 2, Seed: seed + 8}); err != nil {
		return mf, err
	}
	// 8) 深目录（12 层）
	deep := root
	for i := 0; i < 12; i++ {
		deep = filepath.Join(deep, fmt.Sprintf("L%d", i))
	}
	if err := add(filepath.Join(deep, "深层 第1卷.cbz"), testutil.CBZOpts{Pages: 2, Seed: seed + 9}); err != nil {
		return mf, err
	}
	// 9) 单目录多文件（缩小版：300 本，正式环境可调大）
	for i := 0; i < 300; i++ {
		if err := add(filepath.Join(root, "SingleDir", fmt.Sprintf("book%04d.cbz", i)),
			testutil.CBZOpts{Pages: 2, Seed: seed + 1000 + int64(i)}); err != nil {
			return mf, err
		}
	}
	// 10) EPUB 有/无 OPF
	if err := writeEPUBAndCount(filepath.Join(root, "Epubs", "有元数据.epub"), seed+11, b); err != nil {
		return mf, err
	}
	mf.Books++
	mf.Files++
	if err := writeEPUBAndCount(filepath.Join(root, "Epubs", "无元数据.epub"), seed+12, b); err != nil {
		return mf, err
	}
	mf.Books++
	mf.Files++
	mf.Bytes = b.written
	return mf, nil
}

func writeAndCount(path string, opts testutil.CBZOpts, b *budget) error {
	if err := testutil.WriteCBZ(path, opts); err != nil {
		return err
	}
	fi, err := os.Stat(path)
	if err != nil {
		return err
	}
	return b.add(fi.Size())
}

func writeEPUBAndCount(path string, seed int64, b *budget) error {
	if err := testutil.WriteEPUB(path, testutil.EPUBOpts{Title: "测试书", Author: "作者", WithMeta: true, Pages: 3, Seed: seed}); err != nil {
		return err
	}
	fi, err := os.Stat(path)
	if err != nil {
		return err
	}
	return b.add(fi.Size())
}
