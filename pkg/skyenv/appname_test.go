// Package skyenv — pkg/skyenv/appname_test.go: pins the app id → display label
// mapping used on user-facing surfaces.
//
// Worth a test because the label is a silent regression risk: apps stopped
// passing it themselves when notifications moved to the visor-global hub, and
// skychat's notifications had read "Skychat" long before that. A missing case
// here doesn't fail anything — the id just leaks through lowercase into what
// the user sees.
package skyenv

import "testing"

func TestAppDisplayName(t *testing.T) {
	cases := []struct {
		app  string
		want string
	}{
		{SkychatName, "Skychat"},
		{SkysocksClientName, "Skysocks"},
		{VPNClientName, "SkyVPN"},
		{SkydexClientName, "SkyDEX"},
		{"", "Skywire"},
		// An app not yet listed must still read sensibly rather than vanish.
		{"some-future-app", "some-future-app"},
	}
	for _, c := range cases {
		if got := AppDisplayName(c.app); got != c.want {
			t.Errorf("AppDisplayName(%q) = %q, want %q", c.app, got, c.want)
		}
	}
}
