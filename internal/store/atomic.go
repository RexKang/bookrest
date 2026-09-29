// Package store 负责持久化：原子写、镜像（事实/意图）、备份与损坏恢复。
package store

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// WriteFileAtomic 写临时文件 → fsync → rename，保证不会出现半写文件。
func WriteFileAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() {
		if _, statErr := os.Stat(tmpName); statErr == nil {
			_ = os.Remove(tmpName)
		}
	}()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	// Windows 上 rename 到已存在文件会失败，先删除目标（同目录同卷，风险窗口极小）
	if _, err := os.Stat(path); err == nil {
		_ = os.Remove(path)
	}
	return os.Rename(tmpName, path)
}

// BackupFile 把现有文件复制到 backupDir/shelf.<rev>.<ext>（用于滚动备份）。
func BackupFile(path, backupDir, prefix string, rev int64) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil // 首次写入没有可备份的内容
		}
		return "", err
	}
	if err := os.MkdirAll(backupDir, 0o755); err != nil {
		return "", err
	}
	dst := filepath.Join(backupDir, prefix+"."+itoa(rev)+filepath.Ext(path))
	if err := WriteFileAtomic(dst, data); err != nil {
		return "", err
	}
	return dst, nil
}

// RotateBackups 只保留最新的 keep 份备份。
func RotateBackups(backupDir, prefix string, keep int) error {
	entries, err := os.ReadDir(backupDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasPrefix(e.Name(), prefix+".") {
			names = append(names, e.Name())
		}
	}
	if len(names) <= keep {
		return nil
	}
	sort.Strings(names) // 名称前缀相同，按字典序近似按 rev 顺序
	for _, n := range names[:len(names)-keep] {
		_ = os.Remove(filepath.Join(backupDir, n))
	}
	return nil
}

func itoa(v int64) string {
	if v == 0 {
		return "0"
	}
	neg := v < 0
	if neg {
		v = -v
	}
	var b [20]byte
	i := len(b)
	for v > 0 {
		i--
		b[i] = byte('0' + v%10)
		v /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}
