// Package domain 只放纯逻辑：不碰 IO、不依赖具体存储。
//
// 设计约定（见详细设计 §3.1、§5.3）：
//   - 身份由「首 64KB 的哈希 + 文件大小」决定，文件移动/重命名不改变身份
//   - 实现用标准库 crypto/sha256 截断（128 位），避免引入额外依赖；
//     设计文档中的 blake2b-128 语义等价替换为 sha256-128
package domain

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"io"
	"os"
)

// HeadBytes 参与指纹计算的文件头部长度。
const HeadBytes = 64 * 1024

// Fingerprint 描述一个文件的身份来源。
type Fingerprint struct {
	Size  int64  `json:"size"`
	Head  string `json:"head"`  // 首 64KB 的 sha256，截断 16 字节，hex（32 字符）
	MTime int64  `json:"mtime"` // Unix 秒；仅用于变更检测，不参与身份
}

// ID 由 Head + Size 推导出的稳定身份（16 个 hex 字符）。
func (f Fingerprint) ID() string {
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], uint64(f.Size))
	h := sha256.New()
	h.Write([]byte(f.Head))
	h.Write(buf[:])
	sum := h.Sum(nil)
	return hex.EncodeToString(sum[:8])
}

// FingerprintReader 计算 r 的头部哈希与大小。
func FingerprintReader(r io.Reader, size int64) (Fingerprint, error) {
	head := make([]byte, HeadBytes)
	n, err := io.ReadFull(r, head)
	if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
		return Fingerprint{}, err
	}
	head = head[:n]
	sum := sha256.Sum256(head)
	return Fingerprint{Size: size, Head: hex.EncodeToString(sum[:16])}, nil
}

// FingerprintFile 只读打开文件并计算指纹（绝不写入）。
func FingerprintFile(path string) (Fingerprint, error) {
	f, err := os.Open(path) // 只读
	if err != nil {
		return Fingerprint{}, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return Fingerprint{}, err
	}
	fp, err := FingerprintReader(f, st.Size())
	if err != nil {
		return Fingerprint{}, err
	}
	fp.MTime = st.ModTime().Unix()
	return fp, nil
}

// UnstableID 在指纹碰撞（同 ID 不同内容）时使用的降级身份。
// 语义：宁可让身份不稳定，也不能让两本不同的书互相顶替。
func UnstableID(rel string, size, mtime int64) string {
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], uint64(size))
	h := sha256.New()
	h.Write([]byte(rel))
	h.Write(buf[:])
	binary.BigEndian.PutUint64(buf[:], uint64(mtime))
	h.Write(buf[:])
	return hex.EncodeToString(h.Sum(nil)[:8])
}
