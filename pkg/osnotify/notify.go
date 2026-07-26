// Package osnotify posts best-effort desktop notifications to the host OS
// notification center from a background (headless-capable) process.
//
// It is a small, dependency-free, cross-platform helper meant to be shared by
// skywire apps that want to surface an event to a user at their desktop —
// skychat (a new message), and later vpn-client (a connection change), skydex
// (an order filled), etc. Each app just calls:
//
//	osnotify.Notify(osnotify.Notification{Title: "Skychat", Body: "alice: hi"})
//
// Design constraints that shape this package:
//
//   - No new dependencies. Each platform shells out to a tool already present
//     on that OS (macOS osascript, Linux notify-send, Windows PowerShell toast).
//   - Graceful no-op. On a headless server / router / daemon with no desktop
//     session — the common skywire deployment — there is nowhere to post a
//     notification, so Notify returns ErrUnavailable and does nothing. Callers
//     treat that as "couldn't notify", never an error worth surfacing.
//   - Injection-safe. The Body/Title are frequently UNTRUSTED (a chat message
//     from a peer). No backend ever interpolates them into a shell line or a
//     script body: they are passed either as separate argv elements (Linux) or
//     via environment variables the fixed script reads at runtime as data
//     (macOS/Windows). sanitize() is display hygiene, not the security boundary.
package osnotify

import (
	"errors"
	"os/exec"
	"strings"
	"sync"
	"unicode"
)

// ErrUnavailable is returned by Notify when there is no usable desktop
// notification backend — an unsupported platform, a missing notifier binary,
// or (where detectable) no graphical session. It is expected and benign;
// callers should ignore it rather than log it as a failure.
var ErrUnavailable = errors.New("osnotify: no desktop notification backend available")

// Notification is a single desktop notification.
type Notification struct {
	// Title is the bold heading. Empty is replaced with "Skywire".
	Title string
	// Body is the message text. May be untrusted (e.g. a peer's chat message).
	Body string
	// AppName groups/labels notifications where the backend supports it
	// (the Linux app name). Optional; empty means a generic label.
	AppName string
}

// maxField bounds the length of a title/body handed to a backend so an
// oversized message can't bloat an argv or a script. Notification centers
// truncate on display anyway; this is a hard safety cap well above it.
const maxField = 200

// Test seams. Overridden in tests so the suite never posts a real OS
// notification and can assert exactly how untrusted text is passed.
var (
	lookPath = exec.LookPath
	runCmd   = func(c *exec.Cmd) error { return c.Run() }
)

// availability is cached: probing PATH + the session env on every message would
// be wasteful, and it doesn't change over a process's life in practice.
var (
	availMu   sync.Mutex
	availDone bool
	availVal  bool
)

// Available reports whether a usable notification backend exists on this host
// right now (a notifier binary on PATH and, where detectable, a graphical
// session). Cheap after the first call. Apps can use it to log once at startup
// whether desktop notifications will actually fire.
func Available() bool {
	availMu.Lock()
	defer availMu.Unlock()
	if !availDone {
		availVal = available()
		availDone = true
	}
	return availVal
}

// Notify posts n to the OS notification center, best-effort. It returns
// ErrUnavailable when no backend/session is available (the caller should treat
// that as "not shown", not a hard error); any other error is backend-specific.
// It never panics and blocks only for the brief lifetime of the backend
// process — callers that don't want to wait should invoke it in a goroutine.
func Notify(n Notification) error {
	if !Available() {
		return ErrUnavailable
	}
	n.Title = sanitize(n.Title)
	n.Body = sanitize(n.Body)
	if n.Title == "" {
		n.Title = "Skywire"
	}
	return send(n)
}

// sanitize makes a field safe to display: it collapses all runs of whitespace
// (including newlines) to single spaces, drops other control characters, and
// truncates to maxField runes with an ellipsis. It is deliberately conservative
// so the same string renders sensibly across every backend. It is NOT relied on
// for injection safety — the backends pass text as data, never as code.
func sanitize(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	lastSpace := false
	for _, r := range s {
		switch {
		case unicode.IsSpace(r):
			if !lastSpace && b.Len() > 0 {
				b.WriteByte(' ')
				lastSpace = true
			}
		case unicode.IsControl(r):
			// drop
		default:
			b.WriteRune(r)
			lastSpace = false
		}
	}
	out := strings.TrimRight(b.String(), " ")
	r := []rune(out)
	if len(r) > maxField {
		return string(r[:maxField]) + "…"
	}
	return out
}
