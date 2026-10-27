//go:build tools

// Package tools 只用于把代码生成器的依赖钉在 go.mod 里。
// 没有它，go mod tidy 会把 ent 的 CLI 依赖删掉，go generate 在下一次就失败。
package tools

import _ "entgo.io/ent/cmd/ent"
