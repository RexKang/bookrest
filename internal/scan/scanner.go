// Package scan 实现三段式扫描的第一、二段：目录剪枝 + 文件级差异 + 元数据解析。
// 缩略图（第三段）是独立的懒生成流水线（package thumb）。
package scan

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/RexKang/bookrest/internal/domain"
	"github.com/RexKang/bookrest/internal/parse"
	"github.com/RexKang/bookrest/internal/store"
)

// 剪枝的两轮规则（实测驱动，见详细设计 §5.2）：
//
//	Windows/NTFS 上，写入文件后父目录的 mtime 可能在本轮扫描开始后才被补写落定
//	（实测延迟毫秒级）。因此只有「早于本轮扫描开始」的 mtime 才写进状态表；
//	在扫描开始后仍变动的目录记为 0（不稳定），下一轮必然重查。
//	结果是：某目录发生变更后的第一次重扫只做 stat（不哈希、不解析），
//	第二次重扫起即可整棵剪枝 —— 代价很小，且绝不会永久漏检。
//
// Stats 记录一次扫描的可观察行为，供测试断言（剪枝命中、是否真的没打开文件等）。
type Stats struct {
	DirsVisited int
	PrunedDirs  int
	FilesStatted int
	FilesHashed int
	Parsed      int
	Added       int
	Updated     int
	Moved       int
	Missing     int
}

type Diff struct {
	Added   []string
	Updated []string
	Moved   []string
	Missing []string
}

type Scanner struct {
	Root  string
	Stats Stats
}

// ScanIncremental 增量扫描：目录 mtime 未变则整棵子树跳过；
// 目录 mtime 变了才逐文件比对 (size, mtime)，只有变化的才重新计算指纹与解析。
func (s *Scanner) ScanIncremental(idx *store.Index, st *store.ScanState) (Diff, error) {
	var diff Diff
	seen := map[string]bool{}
	now := time.Now().Unix()
	scanStart := time.Now().UnixNano()

	err := filepath.WalkDir(s.Root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // 无权限/瞬时不存在的条目跳过，不中断整次扫描
		}
		rel, rerr := filepath.Rel(s.Root, path)
		if rerr != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			s.Stats.DirsVisited++
			info, ierr := d.Info()
			if ierr != nil {
				return nil
			}
			// 纳秒精度：同一秒内的增删也能被识别（秒级会在快速连续操作时漏检）
			mtime := info.ModTime().UnixNano()
			cur := countEntries(path) // 一次 ReadDir：目录 mtime 可能滞后，条目数是第二道判据
			if rel != "." {
				// 两个判据都满足才允许跳过：
				//  1) mtime 可信（非 0）且与记录一致
				//  2) 直接子项数量与记录一致（增删必然改变数量，即使 mtime 尚未落定）
				if prev, ok := st.Dirs[rel]; ok && prev.MTime != 0 && prev.MTime == mtime && prev.Files == cur {
					s.Stats.PrunedDirs++
					return filepath.SkipDir // 整棵子树跳过
				}
			}
			if st.Dirs == nil {
				st.Dirs = map[string]store.DirInfo{}
			}
			recorded := mtime
			if mtime >= scanStart {
				recorded = 0 // 本轮扫描开始后仍在变动 → 不可信，下轮重查
			}
			st.Dirs[rel] = store.DirInfo{MTime: recorded, Files: cur}
			return nil
		}
		if strings.HasPrefix(d.Name(), ".") {
			return nil // 跳过隐藏文件
		}
		s.Stats.FilesStatted++
		info, ierr := d.Info()
		if ierr != nil {
			return nil
		}
		size := info.Size()
		mtime := info.ModTime().Unix()

		if old, ok := idx.ByRel(rel); ok && old.FP.Size == size && old.FP.MTime == mtime {
			seen[old.ID] = true
			if old.Flags.Missing { // 之前标记缺失，现在又看到了 → 清除标记
				old.Flags.Missing = false
				idx.Put(old)
			}
			return nil // 未变化
		}

		fp, ferr := domain.FingerprintFile(path)
		if ferr != nil {
			return nil
		}
		s.Stats.FilesHashed++
		id := fp.ID()
		fact := store.Fact{
			V: 1, ID: id, FP: fp, Rel: rel,
			Ext:  strings.ToLower(filepath.Ext(rel)),
			Seen: store.Seen{First: now, Last: now},
		}

		// 移动/重命名认领：同 ID 已在索引里但路径不同
		if old, ok := idx.Get(id); ok {
			fact.Seen = old.Seen
			fact.Meta = old.Meta
			fact.Art = old.Art
			fact.Pages = old.Pages
			if old.Rel != rel {
				s.Stats.Moved++
				diff.Moved = append(diff.Moved, rel)
			} else {
				s.Stats.Updated++
				diff.Updated = append(diff.Updated, rel)
			}
		} else {
			s.Stats.Added++
			diff.Added = append(diff.Added, rel)
		}

		if p, ok := parse.ForPath(path); ok {
			if res, perr := p.Parse(path); perr == nil {
				s.Stats.Parsed++
				pages := res.Pages
				fact.Pages = &pages
				fact.Meta = domain.Merge(
					// 用户覆盖由上层在查询时叠加；这里只合并「内嵌 > 文件名」
					domain.Meta{}, res.Meta, domain.ParseFilename(filepath.Base(rel)),
				)
				if fact.Meta.Title == "" {
					fact.Meta.Title = strings.TrimSuffix(filepath.Base(rel), filepath.Ext(rel))
				}
			} else {
				fact.Art.Failed = true // 损坏：进体检，不让扫描失败
			}
		} else {
			// 不支持的格式：仅索引，元数据走文件名
			fact.Meta = domain.ParseFilename(filepath.Base(rel))
			if fact.Meta.Title == "" {
				fact.Meta.Title = filepath.Base(rel)
			}
		}
		idx.Put(fact)
		seen[id] = true
		return nil
	})
	if err != nil {
		return diff, err
	}

	// 缺失：本轮未见到的记录标 missing（不删除，保留摆放）
	var missingIDs []string
	idx.Each(func(f store.Fact) bool {
		if !seen[f.ID] && !f.Flags.Missing {
			f.Flags.Missing = true
			idx.Put(f)
			missingIDs = append(missingIDs, f.ID)
		}
		return true
	})
	s.Stats.Missing = len(missingIDs)
	diff.Missing = missingIDs

	st.LastScan = now
	return diff, nil
}

// countEntries 统计目录的直接子项数量（含子目录）：
// 新增/删除/改名都会改变该数量，用作目录 mtime 之外的第二道剪枝判据。
func countEntries(dir string) int {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return -1 // 不可读 → 与任何记录都不相等，强制下轮重查
	}
	return len(entries)
}
