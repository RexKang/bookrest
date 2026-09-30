// Package frontend 把前端静态资源编进二进制（无需 npm/构建步骤）。
package frontend

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var dist embed.FS

// Assets 返回以 dist 为根的只读文件系统（index.html 位于根）。
func Assets() fs.FS {
	sub, err := fs.Sub(dist, "dist")
	if err != nil {
		panic(err)
	}
	return sub
}
