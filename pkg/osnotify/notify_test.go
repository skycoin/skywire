package osnotify

import (
	"os/exec"
	"strings"
	"testing"
)

// forceAvail pins Available() to v (bypassing the real platform probe) and
// restores the cache on cleanup, so tests are deterministic on any host.
func forceAvail(t *testing.T, v bool) {
	t.Helper()
	availMu.Lock()
	prevDone, prevVal := availDone, availVal
	availDone, availVal = true, v
	availMu.Unlock()
	t.Cleanup(func() {
		availMu.Lock()
		availDone, availVal = prevDone, prevVal
		availMu.Unlock()
	})
}

// captureCmd stubs runCmd to record the command instead of executing it, so no
// real OS notification is posted during the test run.
func captureCmd(t *testing.T) *[]*exec.Cmd {
	t.Helper()
	var got []*exec.Cmd
	prev := runCmd
	runCmd = func(c *exec.Cmd) error { got = append(got, c); return nil }
	t.Cleanup(func() { runCmd = prev })
	return &got
}

func TestSanitize(t *testing.T) {
	cases := map[string]struct{ in, want string }{
		"plain":             {"hello world", "hello world"},
		"collapse spaces":   {"a   b\t\tc", "a b c"},
		"newlines to space": {"line1\nline2\r\nline3", "line1 line2 line3"},
		"strip control":     {"a\x00b\x07c", "abc"},
		"trim edges":        {"  padded  ", "padded"},
		"empty":             {"", ""},
		"only whitespace":   {"   \n\t ", ""},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if got := sanitize(tc.in); got != tc.want {
				t.Errorf("sanitize(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestSanitize_Truncates(t *testing.T) {
	long := strings.Repeat("x", maxField+50)
	got := sanitize(long)
	// maxField runes + a one-rune ellipsis.
	if r := []rune(got); len(r) != maxField+1 || r[maxField] != '…' {
		t.Errorf("truncated length = %d, want %d + ellipsis", len([]rune(got)), maxField)
	}
}

func TestNotify_Unavailable(t *testing.T) {
	forceAvail(t, false)
	got := captureCmd(t)
	if err := Notify(Notification{Title: "T", Body: "B"}); err != ErrUnavailable {
		t.Errorf("Notify on unavailable host = %v, want ErrUnavailable", err)
	}
	if len(*got) != 0 {
		t.Errorf("no backend command should run when unavailable, ran %d", len(*got))
	}
}

// TestNotify_PassesTextAsData is the injection-safety guard: an untrusted body
// must reach the backend as opaque data (an env var or a discrete argv element),
// never concatenated into a shell line or script source, and we must never
// route through a shell interpreter.
func TestNotify_PassesTextAsData(t *testing.T) {
	forceAvail(t, true)
	got := captureCmd(t)

	const evil = `alice: "hi" & bye; rm -rf / $(whoami)`
	if err := Notify(Notification{Title: "Skychat", Body: evil, AppName: "Skychat"}); err != nil {
		t.Fatalf("Notify: %v", err)
	}
	if len(*got) != 1 {
		t.Fatalf("expected exactly one backend command, got %d", len(*got))
	}
	c := (*got)[0]

	// Never invoke a shell — that's where interpolation would be dangerous.
	base := c.Args[0]
	if i := strings.LastIndexAny(base, `/\`); i >= 0 {
		base = base[i+1:]
	}
	for _, shell := range []string{"sh", "bash", "zsh", "cmd", "cmd.exe"} {
		if base == shell {
			t.Fatalf("backend routed through a shell (%q) — untrusted text must not be shell-interpolated", base)
		}
	}

	// The body must appear verbatim as data: either an env value or a single arg.
	inEnv := false
	for _, kv := range c.Env {
		if kv == "OSN_BODY="+evil {
			inEnv = true
		}
	}
	inArgs := false
	for _, a := range c.Args {
		if a == evil {
			inArgs = true
		}
	}
	if !inEnv && !inArgs {
		t.Errorf("untrusted body not passed as data.\n args=%q\n env=%q", c.Args, c.Env)
	}
	// And it must never be embedded inside a larger argv string (i.e. concatenated
	// into a script/command line).
	for _, a := range c.Args {
		if a != evil && strings.Contains(a, evil) {
			t.Errorf("untrusted body embedded inside an argv element: %q", a)
		}
	}
}

func TestAvailable_Cached(t *testing.T) {
	// Available() must return the pinned value without re-probing.
	forceAvail(t, true)
	if !Available() {
		t.Error("Available() = false, want true (pinned)")
	}
	forceAvail(t, false)
	if Available() {
		t.Error("Available() = true, want false (pinned)")
	}
}
