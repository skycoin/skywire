package skynetweb

import (
	"testing"

	"github.com/google/uuid"

	"github.com/skycoin/skywire/pkg/cipher"
)

func mustPK(t *testing.T, hex string) cipher.PubKey {
	t.Helper()
	var pk cipher.PubKey
	if err := pk.Set(hex); err != nil {
		t.Fatalf("bad test PK %q: %v", hex, err)
	}
	return pk
}

func pkLabel(pk cipher.PubKey) RouteLabel { return RouteLabel{PK: pk} }
func tpLabel(id uuid.UUID) RouteLabel     { return RouteLabel{IsTpID: true, TpID: id} }

func TestParseResolverHost(t *testing.T) {
	dest := "0323272a60895f56aad82cb767fb5c413807adcf7c9fb0578b1b1c5807c7f29d4c"
	hop1 := "027087fe40d97f7f0be4a0dc768462ddbb371d4b9e7679d4f11f117d757b9856ed"
	hop2 := "02f9aa588dffa20b205e1c10bd0236130f080af157044d0eaa35753d2f2fcd6c36"
	tp1 := "8d91173a-6101-0989-ba81-684a359c0cd9"

	dp := mustPK(t, dest)
	h1 := mustPK(t, hop1)
	h2 := mustPK(t, hop2)
	t1 := uuid.MustParse(tp1)

	cases := []struct {
		name      string
		host      string
		suffix    string
		wantVhost string
		wantRoute []RouteLabel
		wantDest  cipher.PubKey
		wantPort  uint16
		wantErr   bool
	}{
		{"bare dest", dest + ".dmsg", ".dmsg", "", nil, dp, 80, false},
		{"vhost + dest", "example.com." + dest + ".dmsg", ".dmsg", "example.com", nil, dp, 80, false},
		{"pinned server (PK)", "example.com." + hop1 + "." + dest + ".dmsg", ".dmsg", "example.com", []RouteLabel{pkLabel(h1)}, dp, 80, false},
		{"two-hop skynet PKs (source order)", "site." + hop1 + "." + hop2 + "." + dest + ".skynet", ".skynet", "site", []RouteLabel{pkLabel(h1), pkLabel(h2)}, dp, 80, false},
		{"tpid hop", "site." + tp1 + "." + dest + ".skynet", ".skynet", "site", []RouteLabel{tpLabel(t1)}, dp, 80, false},
		{"mixed tpid + pk hop", tp1 + "." + hop2 + "." + dest + ".skynet", ".skynet", "", []RouteLabel{tpLabel(t1), pkLabel(h2)}, dp, 80, false},
		{"explicit port", dest + ".dmsg:8080", ".dmsg", "", nil, dp, 8080, false},
		{"wrong suffix", dest + ".onion", ".dmsg", "", nil, cipher.PubKey{}, 0, true},
		{"bad dest", "notapk.dmsg", ".dmsg", "", nil, cipher.PubKey{}, 0, true},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			vhost, route, gotDest, port, err := ParseResolverHost(c.host, c.suffix)
			if c.wantErr {
				if err == nil {
					t.Fatalf("expected error, got none")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if vhost != c.wantVhost {
				t.Errorf("vhost = %q, want %q", vhost, c.wantVhost)
			}
			if port != c.wantPort {
				t.Errorf("port = %d, want %d", port, c.wantPort)
			}
			if gotDest != c.wantDest {
				t.Errorf("dest = %s, want %s", gotDest, c.wantDest)
			}
			if len(route) != len(c.wantRoute) {
				t.Fatalf("route len = %d, want %d (%v)", len(route), len(c.wantRoute), route)
			}
			for i := range route {
				if route[i] != c.wantRoute[i] {
					t.Errorf("route[%d] = %+v, want %+v", i, route[i], c.wantRoute[i])
				}
			}
		})
	}
}
