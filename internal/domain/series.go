package domain

import (
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

var (
	// 第01卷 / 第 3 話 / 第12册
	reVolumeCN = regexp.MustCompile(`第\s*([0-9]+(?:\.[0-9]+)?)\s*[卷話话册集部]`)
	// v01 / V01
	reVolumeV = regexp.MustCompile(`(?i)\bv\s*([0-9]+(?:\.[0-9]+)?)\b`)
	// Vol.01 / Volume 3
	reVolumeVol = regexp.MustCompile(`(?i)\bvol(?:ume)?\.?\s*([0-9]+(?:\.[0-9]+)?)`)
	// #01 / - 01
	reHashNum = regexp.MustCompile(`#\s*([0-9]+(?:\.[0-9]+)?)`)
	reTailNum = regexp.MustCompile(`[\s\-_]+([0-9]{1,3})(?:\s*[\(\[]|$)`)
	// 括号组：[汉化组] (2020) 【作者】
	reBracket = regexp.MustCompile(`[\[\(【（][^\]\)】）]*[\]\)】）]`)
)

// ParseFilename 从文件名解析系列与卷号（FR-4.2）。
// 规则尽量宽松：解析失败时返回只带 Title 的结果，由上层决定是否展示。
func ParseFilename(filename string) Meta {
	stem := strings.TrimSuffix(filename, filepath.Ext(filename))
	title := strings.TrimSpace(stem)

	// 1) 抽卷号：按优先级依次尝试
	// useGroupEnd：该模式会吞掉后面的括号（如 " - 07 (2020)" 中的 "("），
	// 因此用「捕获组结束位置」而非「整体匹配结束位置」切分剩余文本
	type volPattern struct {
		re          *regexp.Regexp
		useGroupEnd bool
	}
	number := ""
	series := stem
	for _, vp := range []volPattern{
		{reVolumeCN, false}, {reVolumeVol, false}, {reVolumeV, false},
		{reHashNum, false}, {reTailNum, true},
	} {
		if m := vp.re.FindStringSubmatchIndex(stem); m != nil {
			number = stem[m[2]:m[3]]
			end := m[1]
			if vp.useGroupEnd {
				end = m[3]
			}
			series = strings.TrimSpace(stem[:m[0]] + " " + stem[end:])
			break
		}
	}

	// 2) 去掉括号组、多余分隔符
	series = reBracket.ReplaceAllString(series, " ")
	series = strings.Trim(series, " -_·．.")
	series = strings.Join(strings.Fields(series), " ")

	// 3) 归一化卷号：01 → 1，去掉小数尾零
	if number != "" {
		if f, err := strconv.ParseFloat(number, 64); err == nil {
			if f == float64(int64(f)) {
				number = strconv.FormatInt(int64(f), 10)
			} else {
				number = strconv.FormatFloat(f, 'f', -1, 64)
			}
		}
	}

	m := Meta{Title: title, Series: series, Number: number, Src: SrcFilename}
	if series == "" {
		m.Series = title
	}
	return m
}

// NumberValue 把卷号字符串转成数值，供断档检测使用。
func NumberValue(s string) (float64, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, false
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, false
	}
	return f, true
}

// FindGaps 在给定卷号集合中找出整数断档（FR-5.1）。
// 只报告整数区间内的缺口：例如 [1..8, 10] → [9]；非整数卷号忽略。
func FindGaps(numbers []float64) []int {
	if len(numbers) == 0 {
		return nil
	}
	seen := map[int]bool{}
	min, max := 0, 0
	first := true
	for _, n := range numbers {
		if n != float64(int64(n)) { // 非整数卷号不参与断档判断
			continue
		}
		v := int(n)
		if v <= 0 {
			continue
		}
		seen[v] = true
		if first || v < min {
			min = v
		}
		if first || v > max {
			max = v
		}
		first = false
	}
	if first || max-min < 2 {
		return nil
	}
	var gaps []int
	for v := min; v <= max; v++ {
		if !seen[v] {
			gaps = append(gaps, v)
		}
	}
	sort.Ints(gaps)
	return gaps
}
