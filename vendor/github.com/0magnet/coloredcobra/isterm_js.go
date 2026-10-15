//go:build js

package coloredcobra

import "os"

// isTerminal follows the rule colorenable_js.go applies on the standard
// toolchain: stdout under js/wasm is always a pipe, so the host says whether
// the other end draws ANSI by exporting TERM. NO_COLOR and TERM=dumb are
// checked by colorEnabled before this is asked.
func isTerminal(_ uintptr) bool { return os.Getenv("TERM") != "" }
