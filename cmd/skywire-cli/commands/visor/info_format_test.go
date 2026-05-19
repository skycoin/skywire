// Package clivisor — cmd/skywire-cli/commands/visor/info_format_test.go:
// table-driven tests for the AR self-registration display path. Pins
// the rendering of dual-stack entries (#1525 Phase 4a) and the
// pre-existing single-stack semantics that callers relied on before
// RemoteAddrV6 was added.

package clivisor

import (
	"testing"

	"github.com/skycoin/skywire/pkg/visor"
)

func TestFormatARAddr(t *testing.T) {
	cases := []struct {
		name string
		in   visor.ARSelfEntry
		want string
	}{
		{
			name: "v4 only, host:port in RemoteAddr",
			in:   visor.ARSelfEntry{Type: "stcpr", RemoteAddr: "1.2.3.4:7777", Port: "7777"},
			want: "1.2.3.4:7777",
		},
		{
			name: "v4 only, bare IP in RemoteAddr + Port",
			in:   visor.ARSelfEntry{Type: "stcpr", RemoteAddr: "1.2.3.4", Port: "7777"},
			want: "1.2.3.4:7777",
		},
		{
			name: "v4 only, NAT-mapped port differs from listen port",
			in:   visor.ARSelfEntry{Type: "sudph", RemoteAddr: "1.2.3.4:55555", Port: "7777"},
			want: "1.2.3.4:55555 (listen :7777)",
		},
		{
			name: "no v4, no v6 (degenerate)",
			in:   visor.ARSelfEntry{Type: "stcpr"},
			want: "(unknown)",
		},
		{
			name: "v4 plus v6 — dual stack with explicit family labels",
			in: visor.ARSelfEntry{
				Type:         "stcpr",
				RemoteAddr:   "1.2.3.4:7777",
				RemoteAddrV6: "[2001:db8::1]:7777",
				Port:         "7777",
			},
			want: "1.2.3.4:7777 [v4] / [2001:db8::1]:7777 [v6]",
		},
		{
			name: "v6 only with Port set — half-bound startup state",
			// Dual-stack visor startup race: v6 bind completed before
			// v4. Existing v4 rendering already falls back to port-
			// only when RemoteAddr is empty but Port is set. The v6
			// side is rendered alongside so operators can still see
			// it IS attached even though v4 hasn't bound yet.
			in: visor.ARSelfEntry{
				Type:         "stcpr",
				RemoteAddrV6: "[2001:db8::1]:7777",
				Port:         "7777",
			},
			want: "7777 [v4] / [2001:db8::1]:7777 [v6]",
		},
		{
			name: "v6 only with no Port — fully half-bound",
			// Both RemoteAddr and Port empty on the v4 side; the v4
			// path renders as "(unknown)" so the operator can see the
			// v4 bind never happened, while v6 is still surfaced.
			in: visor.ARSelfEntry{
				Type:         "stcpr",
				RemoteAddrV6: "[2001:db8::1]:7777",
			},
			want: "(unknown) [v4] / [2001:db8::1]:7777 [v6]",
		},
		{
			name: "v4 plus v6, NAT-mapped port on v4",
			in: visor.ARSelfEntry{
				Type:         "sudph",
				RemoteAddr:   "1.2.3.4:55555",
				RemoteAddrV6: "[2001:db8::1]:7777",
				Port:         "7777",
			},
			want: "1.2.3.4:55555 (listen :7777) [v4] / [2001:db8::1]:7777 [v6]",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := formatARAddr(c.in)
			if got != c.want {
				t.Errorf("formatARAddr(%+v):\n  got:  %q\n  want: %q", c.in, got, c.want)
			}
		})
	}
}
