// Package report 生成只读体检报告：缺卷、重复、损坏、无封面、命名待整理。
// 只读：不修改任何文件（FR-5.1~5.4）。
package report

import (
	"sort"
	"strconv"
	"strings"

	"github.com/RexKang/bookrest/internal/domain"
	"github.com/RexKang/bookrest/internal/parse"
	"github.com/RexKang/bookrest/internal/store"
)

type Kind string

const (
	KindGap     Kind = "gap"     // 缺卷
	KindDup     Kind = "dup"     // 重复
	KindCorrupt Kind = "corrupt" // 损坏
	KindNoCover Kind = "nocover" // 无封面
	KindNaming  Kind = "naming"  // 命名待整理
)

type Finding struct {
	Kind    Kind     `json:"kind"`
	Series  string   `json:"series,omitempty"`
	Missing []int    `json:"missing,omitempty"`
	Items   []string `json:"items,omitempty"`
	Detail  string   `json:"detail,omitempty"`
}

type Report struct {
	Root     string    `json:"root"`
	Total    int       `json:"total"`
	Findings []Finding `json:"findings"`
}

func (r *Report) Count(k Kind) int {
	n := 0
	for _, f := range r.Findings {
		if f.Kind == k {
			n++
		}
	}
	return n
}

// Run 遍历事实账，产出报告。全部为只读分析。
// Run 生成体检报告。overrides 用于识别「用户已手动指定封面」的条目，
// 避免它们被计入「无封面」。
func Run(root string, idx *store.Index, overrides map[string]store.Override) *Report {
	// Findings 初始化为空切片：JSON 序列化成 [] 而不是 null，界面与脚本都少一层判断
	rep := &Report{Root: root, Findings: []Finding{}}

	seriesNums := map[string][]float64{}
	seriesItems := map[string][]string{}
	var corrupt, noCover, naming []string
	var dups []Finding

	idx.Each(func(f store.Fact) bool {
		if f.Flags.Missing {
			return true
		}
		rep.Total++
		if f.Meta.Series != "" {
			if n, ok := domain.NumberValue(f.Meta.Number); ok {
				seriesNums[f.Meta.Series] = append(seriesNums[f.Meta.Series], n)
			}
			seriesItems[f.Meta.Series] = append(seriesItems[f.Meta.Series], f.Rel)
		}
		if f.Art.Failed {
			corrupt = append(corrupt, f.Rel)
		}
		// 「无封面」只统计**本该能取到封面**的格式（CBZ/EPUB 等有解析器的）。
		// PDF/CBR/MOBI 这类只索引的格式天然取不到封面，计进来只会淹没报告。
		if !f.Art.Cover && !f.Art.Failed && parse.CanExtractCover(f.Ext) {
			if ov, ok := overrides[f.ID]; !ok || ov.Cover == nil {
				noCover = append(noCover, f.Rel)
			}
		}
		if needsNaming(f) {
			naming = append(naming, f.Rel)
		}
		if len(f.Dups) > 0 {
			dups = append(dups, Finding{Kind: KindDup, Items: append([]string{f.Rel}, f.Dups...)})
		}
		return true
	})

	// 缺卷
	var seriesNames []string
	for s := range seriesNums {
		seriesNames = append(seriesNames, s)
	}
	sort.Strings(seriesNames)
	for _, s := range seriesNames {
		gaps := domain.FindGaps(seriesNums[s])
		if len(gaps) == 0 {
			continue
		}
		items := append([]string(nil), seriesItems[s]...)
		sort.Strings(items)
		rep.Findings = append(rep.Findings, Finding{Kind: KindGap, Series: s, Missing: gaps, Items: items})
	}

	sortFindings(dups)
	rep.Findings = append(rep.Findings, dups...)
	if len(corrupt) > 0 {
		sort.Strings(corrupt)
		rep.Findings = append(rep.Findings, Finding{Kind: KindCorrupt, Items: corrupt, Detail: "归档无法解析（截断/CRC/伪装扩展名）"})
	}
	if len(noCover) > 0 {
		sort.Strings(noCover)
		rep.Findings = append(rep.Findings, Finding{Kind: KindNoCover, Items: noCover, Detail: "归档内没有图片项"})
	}
	if len(naming) > 0 {
		sort.Strings(naming)
		rep.Findings = append(rep.Findings, Finding{Kind: KindNaming, Items: naming, Detail: "文件名含括号噪声或过长，建议规范化"})
	}
	return rep
}

// needsNaming 判定「命名待整理」：来自文件名解析且含噪声或过长。
func needsNaming(f store.Fact) bool {
	if f.Meta.Src != domain.SrcFilename {
		return false
	}
	base := f.Rel
	if i := strings.LastIndex(base, "/"); i >= 0 {
		base = base[i+1:]
	}
	if len([]rune(base)) > 80 {
		return true
	}
	return strings.ContainsAny(base, "【】[]{}")
}

func sortFindings(fs []Finding) {
	sort.Slice(fs, func(i, j int) bool { return len(fs[i].Items) > 0 && len(fs[j].Items) > 0 && fs[i].Items[0] < fs[j].Items[0] })
}

// NumberString 便于展示卷号列表。
func NumberString(nums []int) string {
	parts := make([]string, 0, len(nums))
	for _, n := range nums {
		parts = append(parts, strconv.Itoa(n))
	}
	return strings.Join(parts, ", ")
}
