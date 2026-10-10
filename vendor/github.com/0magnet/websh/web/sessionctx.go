//go:build js && wasm

package web

import (
	"context"

	"github.com/0magnet/websh/shell"
)

// SessionForContext returns the session whose shell dispatched this command,
// or nil outside one.
//
// It is what Options.Exec needs and the only thing it was missing. The hook is
// given a context and an argv; a full-screen command reached through it has to
// draw on the terminal it was typed into, and this is how it says which that
// is without the embedder keeping a package variable that is wrong the moment
// a second terminal opens.
func SessionForContext(ctx context.Context) *Session {
	return SessionFor(shell.FromContext(ctx))
}
