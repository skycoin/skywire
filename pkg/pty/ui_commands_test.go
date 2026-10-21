//go:build !mobile

package pty

import (
	"net/http/httptest"
	"testing"
)

// TestURLCommandsRunOnlyNamed checks that ?commands= runs the visor's named
// commands but only types anything else, so a link cannot run a command.
func TestURLCommandsRunOnlyNamed(t *testing.T) {
	named := map[string][]string{"update": {"apt update", "apt upgrade"}}
	cases := []struct{ query, want string }{
		{"", ""},
		{"?commands=update", "apt update && apt upgrade\n"},
		{"?commands=skywire%20cli%20gotop%20--local", "skywire cli gotop --local"},
		{"?commands=update,rm%20-rf%20x", "apt update && apt upgrade && rm -rf x"},
	}
	for _, tc := range cases {
		if got := urlCommands(httptest.NewRequest("GET", "/pty"+tc.query, nil), named); got != tc.want {
			t.Errorf("%q: got %q, want %q", tc.query, got, tc.want)
		}
	}
}

// TestTermPageRefusesFraming checks that the terminal page cannot be framed by
// another site.
func TestTermPageRefusesFraming(t *testing.T) {
	rec := httptest.NewRecorder()
	NewUI(NetUIDialer("unix", "/nonexistent"), DefaultUIConfig()).Handler(nil)(rec, httptest.NewRequest("GET", "/pty", nil))
	if got := rec.Header().Get("Content-Security-Policy"); got != "frame-ancestors 'self'" {
		t.Errorf("Content-Security-Policy = %q", got)
	}
	if got := rec.Header().Get("X-Frame-Options"); got != "SAMEORIGIN" {
		t.Errorf("X-Frame-Options = %q", got)
	}
}
