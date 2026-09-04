//go:build !(js && wasm)

package shell

import "context"

// execExternal is a no-op off js/wasm: websh is a self-contained shell there,
// with no process layer to exec into. Returns handled=false so the caller
// reports "command not found".
func (s *Shell) execExternal(ctx context.Context, args []string) (code int, handled bool) {
	return 0, false
}
