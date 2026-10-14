package vpn

import (
	"testing"

	"github.com/skycoin/skywire/pkg/app/appnet"
	"github.com/skycoin/skywire/pkg/routing"
)

func TestPickResolverPort(t *testing.T) {
	svcs := []appnet.LocalService{
		{Port: 44, Label: "pty"},
		{Port: 4445, Label: "dmsg_web", Suffixes: []string{".dmsg"}},
		{Port: 4446, Label: "skynet_web", Suffixes: []string{".skynet"}},
	}
	for scheme, want := range map[string]routing.Port{"dmsg": 4445, "skynet": 4446} {
		if got, ok := pickResolverPort(svcs, scheme); !ok || got != want {
			t.Errorf("pickResolverPort(%s) = %d, %v; want %d", scheme, got, ok, want)
		}
	}
	if _, ok := pickResolverPort(svcs[:1], "skynet"); ok {
		t.Error("no resolver published must mean a direct dial")
	}
}
