package visor

import (
	"testing"

	"github.com/skycoin/skywire/pkg/proxystatus"
)

func TestMeshStatusSurface(t *testing.T) {
	const suffix = ".haltingstate.net"
	cases := []struct {
		host    string
		want    proxystatus.Surface
		matched bool
	}{
		{"status-skysocks.haltingstate.net", proxystatus.SurfaceSkysocks, true},
		{"status-dmsg.haltingstate.net", proxystatus.SurfaceDmsg, true},
		{"status-skynet.haltingstate.net", proxystatus.SurfaceSkynet, true},
		{"STATUS-SkySocks.haltingstate.net", proxystatus.SurfaceSkysocks, true},
		{"status-skysocks.haltingstate.net:8443", "", false}, // caller strips port; a raw :port here is not matched
		{"status-unknown.haltingstate.net", "", false},
		{"vhost.abcd.haltingstate.net", "", false},        // ordinary browse frame
		{"status-skysocks.x.haltingstate.net", "", false}, // multi-label, not wildcard-safe
		{"status-skysocks.example.com", "", false},        // wrong suffix
		{"status-skysocks", "", false},                    // no suffix
	}
	for _, c := range cases {
		got, ok := meshStatusSurface(c.host, suffix)
		if ok != c.matched || got != c.want {
			t.Errorf("meshStatusSurface(%q) = (%q,%v), want (%q,%v)", c.host, got, ok, c.want, c.matched)
		}
	}
}

func TestMeshStatusSurfaceLocalhostSuffix(t *testing.T) {
	// The default (local) suffix also matches, so status works over http locally.
	if s, ok := meshStatusSurface("status-skysocks.mesh.localhost", ".mesh.localhost"); !ok || s != proxystatus.SurfaceSkysocks {
		t.Errorf("localhost suffix: got (%q,%v)", s, ok)
	}
}
