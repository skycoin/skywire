// Package visor pkg/visor/api_skychat_addr_test.go c3-vis-core
//
// Where the visor thinks skychat is listening, and what it will accept as a
// new answer. Both sides of that question changed when skychat started
// binding a port by default, and both are read by the hypervisor UI — one to
// draw a link, the other to save an edit — so a disagreement between them
// shows up as a link to nowhere or a save that refuses its own default.
package visor

import (
	"testing"

	"github.com/skycoin/skywire/pkg/app/appserver"
	"github.com/skycoin/skywire/pkg/skyenv"
	"github.com/skycoin/skywire/pkg/visor/visorconfig"
)

// visorWithSkychatArgs builds the smallest Visor that skychatLocalAddr reads:
// a config with one launcher app.
func visorWithSkychatArgs(args []string) *Visor {
	return &Visor{
		conf: &visorconfig.V1{
			Launcher: &visorconfig.Launcher{
				Apps: []appserver.AppConfig{{Name: skyenv.SkychatName, Args: args}},
			},
		},
	}
}

func TestSkychatLocalAddrReportsWhatSkychatBinds(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want string
	}{
		// The generated default. Reported verbatim — it is already a
		// dialable host:port.
		{"explicit host", []string{"--addr", "127.0.0.1:8001"}, "127.0.0.1:8001"},
		// The docker form. Rewritten to loopback because this address is
		// used by a client inside the visor process, not by the listener.
		{"all interfaces", []string{"--addr", "*:8001", "--pair-enable"}, "127.0.0.1:8001"},
		{"port only", []string{"--addr", ":8080"}, "127.0.0.1:8080"},
		// No --addr at all: the app's own flag default applies, which is
		// what skyenv.SkychatAddr names.
		{"no addr arg", []string{"--pair-enable"}, skyenv.SkychatAddr},
		// Nothing is listening, so there is no address to report. Reporting
		// the default here is what used to put a dead link in the UI.
		{"portless", []string{"--portless", "--pair-enable"}, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := skychatLocalAddr(visorWithSkychatArgs(c.args)); got != c.want {
				t.Fatalf("skychatLocalAddr(%v) = %q, want %q", c.args, got, c.want)
			}
		})
	}
}

// The hypervisor reads the current address, shows it, and saves what comes
// back. If the generator's own default cannot survive that round trip, an
// untouched save fails — which is what happened when this validator accepted
// only ":PORT" and "*:PORT".
func TestSetAppAddressAcceptsTheGeneratedDefault(t *testing.T) {
	for _, addr := range []string{
		skyenv.SkychatAddr, // "127.0.0.1:8001" — the generated default
		"127.0.0.1:8001",
		"localhost:8001",
		"[::1]:8001",
		":8001",
		"*:8001",
	} {
		if err := validateAppAddress(addr); err != nil {
			t.Errorf("validateAppAddress(%q) = %v, want accepted", addr, err)
		}
	}
}

func TestSetAppAddressRejectsNonsense(t *testing.T) {
	for _, addr := range []string{
		"",
		"8001",             // no colon: not an address
		":80",              // privileged port
		":70000",           // out of range
		"example.com:8001", // a name the visor cannot bind
		"127.0.0.1",        // no port
		"127.0.0.1:notaport",
	} {
		if err := validateAppAddress(addr); err == nil {
			t.Errorf("validateAppAddress(%q) accepted, want rejection", addr)
		}
	}
}
