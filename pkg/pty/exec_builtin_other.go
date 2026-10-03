//go:build !(js && wasm)

// Package pty pkg/pty/exec_builtin_other.go c3-vis-pty
package pty

import (
	"context"
	"io"
)

// execBuiltin is the browser's stand-in for starting a process; a native host
// starts real ones.
func execBuiltin(context.Context, *CommandExecReq, io.Writer, io.Writer) (int, bool) {
	return 0, false
}
