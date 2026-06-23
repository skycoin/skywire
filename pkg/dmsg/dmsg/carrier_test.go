package dmsg

import (
	"testing"

	"github.com/skycoin/skywire/pkg/dmsg/disc"
)

// TestPickCarrier locks the dmsg carrier-selection semantics: the first listed
// carrier the server advertises wins; empty/no-match falls back to QUIC (when
// advertised) else TCP; an explicit "tcp" wins even when QUIC is advertised.
func TestPickCarrier(t *testing.T) {
	full := &disc.Entry{
		Protocol: "quic",
		Server: &disc.Server{
			Address:    "1.2.3.4:8081",
			AddressWS:  "ws://1.2.3.4:8083/dmsg",
			AddressWT:  "https://1.2.3.4:8084/dmsg",
			AddressUDP: "1.2.3.4:8085",
		},
	}
	tcpOnly := &disc.Entry{Server: &disc.Server{Address: "1.2.3.4:8081"}}

	cases := []struct {
		name     string
		carriers []string
		entry    *disc.Entry
		wantNet  string
		wantAddr string
	}{
		{"empty defaults to quic when advertised", nil, full, "quic", "1.2.3.4:8085"},
		{"empty falls to tcp without quic", nil, tcpOnly, "tcp", "1.2.3.4:8081"},
		{"ws preferred", []string{"ws"}, full, "ws", "ws://1.2.3.4:8083/dmsg"},
		{"wt preferred", []string{"wt"}, full, "wt", "https://1.2.3.4:8084/dmsg"},
		{"explicit tcp beats advertised quic", []string{"tcp"}, full, "tcp", "1.2.3.4:8081"},
		{"first advertised wins (wt before ws)", []string{"wt", "ws"}, full, "wt", "https://1.2.3.4:8084/dmsg"},
		{"skip unavailable, take next", []string{"wt", "ws"}, tcpOnly, "tcp", "1.2.3.4:8081"},
		{"unknown name ignored, default applies", []string{"bogus"}, full, "quic", "1.2.3.4:8085"},
		{"ws requested but not advertised → default", []string{"ws"}, tcpOnly, "tcp", "1.2.3.4:8081"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			gotNet, gotAddr := pickCarrier(c.carriers, c.entry)
			if gotNet != c.wantNet || gotAddr != c.wantAddr {
				t.Fatalf("pickCarrier(%v) = (%q,%q), want (%q,%q)", c.carriers, gotNet, gotAddr, c.wantNet, c.wantAddr)
			}
		})
	}
}
