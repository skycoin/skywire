package shell

import "context"

// Which shell an Exec command is running under.
//
// Exec is handed a context and an argv, which is everything an ordinary
// command needs. A FULL-SCREEN one needs more: to take raw mode, to ask how
// big the terminal is, to find the terminal at all. All three hang off the
// *Shell, and the embedder cannot simply remember the one it built — a page
// can hold several terminals, and then the command running in the second one
// drives the first.
//
// So the shell puts itself in the context it dispatches with, and the
// embedder's hook asks the context which shell called it. The same question
// websh's own applets answer by being handed the *Shell directly.

type shellKey struct{}

// WithShell returns ctx carrying sh.
func WithShell(ctx context.Context, sh *Shell) context.Context {
	return context.WithValue(ctx, shellKey{}, sh)
}

// FromContext returns the shell that dispatched this command, or nil in a
// context that did not come from one.
func FromContext(ctx context.Context) *Shell {
	sh, _ := ctx.Value(shellKey{}).(*Shell)
	return sh
}
