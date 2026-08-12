// Package clivisor — cmd/skywire-cli/commands/visor/info_format_test.go:
// table-driven tests for the AR self-registration display path. Pins
// the rendering of dual-stack entries (#1525 Phase 4a) and the
// pre-existing single-stack semantics that callers relied on before
// RemoteAddrV6 was added.

package clivisor

import (
	"strings"
	"testing"
	"time"

	"github.com/skycoin/skywire/pkg/visor/dmsgtracker"

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

func TestAbbrevHash(t *testing.T) {
	const full = "9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08"
	got := abbrevHash(full)
	if got != "9f86d081…b0f00a08" {
		t.Fatalf("abbrevHash(%q) = %q", full, got)
	}
	// Short enough to show whole: left alone rather than padded or cut.
	if s := abbrevHash("abc123"); s != "abc123" {
		t.Fatalf("abbrevHash short = %q, want it unchanged", s)
	}
}

func TestPluralVisors(t *testing.T) {
	for n, want := range map[int]string{0: "0 visors", 1: "1 visor", 7: "7 visors"} {
		if got := pluralVisors(n); got != want {
			t.Errorf("pluralVisors(%d) = %q, want %q", n, got, want)
		}
	}
}

// A latency the visor never measured must not be reported as zero. The
// tracker keys on the visor's own PK and usually holds no entry for it, so the
// summary carries a zero value that means "unknown", not "instant".
func TestDmsgLatencyUnmeasured(t *testing.T) {
	if got := dmsgLatency(&visor.Summary{}); got != "" {
		t.Errorf("nil DmsgStats: got %q, want empty", got)
	}
	zero := &visor.Summary{DmsgStats: &dmsgtracker.DmsgClientSummary{}}
	if got := dmsgLatency(zero); got != "" {
		t.Errorf("zero RoundTrip: got %q, want empty", got)
	}
	measured := &visor.Summary{DmsgStats: &dmsgtracker.DmsgClientSummary{
		RoundTrip: 8 * time.Millisecond,
	}}
	if got := dmsgLatency(measured); got != "8ms" {
		t.Errorf("measured: got %q, want 8ms", got)
	}
}

// The point of ARSelfRegistration.Queried: "asked and not registered" and
// "never asked" must not render the same. Only the first is an answer.
func TestRenderARSection(t *testing.T) {
	stcpr := visor.ARSelfEntry{Type: "stcpr", RemoteAddr: "1.2.3.4:7777"}
	wt := visor.ARSelfEntry{
		Type:       "wt",
		RemoteAddr: "1.2.3.4:7773",
		CertHash:   "9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08",
	}

	t.Run("modern visor, WT checked and unregistered, says so", func(t *testing.T) {
		got := renderARSection(&visor.ARSelfRegistration{
			Entries: []visor.ARSelfEntry{stcpr},
			Queried: []string{"stcpr", "sudph", "wt"},
		})
		for _, want := range []string{"SUDPH  (not registered)", "WT     (not registered)"} {
			if !strings.Contains(got, want) {
				t.Errorf("missing %q in:\n%s", want, got)
			}
		}
	})

	t.Run("old visor reports no Queried, so WT is not mentioned", func(t *testing.T) {
		got := renderARSection(&visor.ARSelfRegistration{
			Entries: []visor.ARSelfEntry{stcpr},
		})
		if strings.Contains(got, "WT") || strings.Contains(got, "not registered") {
			t.Errorf("claimed something about WT from a visor that never mentioned it:\n%s", got)
		}
	})

	t.Run("WT registration shows the cert hash abbreviated", func(t *testing.T) {
		got := renderARSection(&visor.ARSelfRegistration{
			Entries: []visor.ARSelfEntry{wt},
			Queried: []string{"wt"},
		})
		if !strings.Contains(got, "(cert 9f86d081…b0f00a08)") {
			t.Errorf("cert hash not rendered:\n%s", got)
		}
	})

	t.Run("nothing registered stays a single line", func(t *testing.T) {
		got := renderARSection(&visor.ARSelfRegistration{Queried: []string{"stcpr", "wt"}})
		if got != "AR Registration: (none)\n" {
			t.Errorf("got %q", got)
		}
	})
}
