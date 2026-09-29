package domain

// MetaSource 标记元数据来自哪一层，决定覆盖优先级（详细设计 §3.1、FR-4.3）。
type MetaSource string

const (
	SrcUser     MetaSource = "user"     // 用户覆盖（意图账）
	SrcComicInfo MetaSource = "comicinfo" // CBZ 内嵌
	SrcOPF      MetaSource = "opf"      // EPUB 内嵌
	SrcXMP      MetaSource = "xmp"      // PDF 内嵌
	SrcFilename MetaSource = "filename" // 文件名解析
)

// Meta 是一本书的展示元数据。字段级合并，优先级：用户 > 内嵌 > 文件名。
type Meta struct {
	Title  string     `json:"title,omitempty"`
	Series string     `json:"series,omitempty"`
	Number string     `json:"number,omitempty"`
	Author string     `json:"author,omitempty"`
	Artist string     `json:"artist,omitempty"`
	Year   int        `json:"year,omitempty"`
	Src    MetaSource `json:"src,omitempty"`
}

// priority 越小优先级越高。
func priority(s MetaSource) int {
	switch s {
	case SrcUser:
		return 0
	case SrcComicInfo, SrcOPF, SrcXMP:
		return 1
	case SrcFilename:
		return 2
	default:
		return 3
	}
}

// Merge 按优先级做字段级合并：每个字段取优先级最高且非空的值。
// 返回的 Src 记录「实际贡献了至少一个字段的最高优先级来源」。
func Merge(sources ...Meta) Meta {
	out := Meta{}
	// 按优先级从高到低逐个字段填充
	ordered := append([]Meta(nil), sources...)
	for i := 0; i < len(ordered); i++ {
		for j := i + 1; j < len(ordered); j++ {
			if priority(ordered[j].Src) < priority(ordered[i].Src) {
				ordered[i], ordered[j] = ordered[j], ordered[i]
			}
		}
	}
	contributed := MetaSource("")
	fill := func(dst *string, get func(Meta) string) {
		if *dst != "" {
			return
		}
		for _, s := range ordered {
			if v := get(s); v != "" {
				*dst = v
				if contributed == "" || priority(s.Src) < priority(contributed) {
					contributed = s.Src
				}
				return
			}
		}
	}
	fill(&out.Title, func(m Meta) string { return m.Title })
	fill(&out.Series, func(m Meta) string { return m.Series })
	fill(&out.Number, func(m Meta) string { return m.Number })
	fill(&out.Author, func(m Meta) string { return m.Author })
	fill(&out.Artist, func(m Meta) string { return m.Artist })
	for _, s := range ordered { // Year 是数值，单独处理
		if out.Year == 0 && s.Year != 0 {
			out.Year = s.Year
			if contributed == "" || priority(s.Src) < priority(contributed) {
				contributed = s.Src
			}
			break
		}
	}
	out.Src = contributed
	if out.Src == "" {
		out.Src = SrcFilename
	}
	return out
}
