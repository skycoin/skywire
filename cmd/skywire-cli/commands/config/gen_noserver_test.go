package cliconfig

import (
	"testing"

	"github.com/skycoin/skywire/pkg/app/appserver"
	"github.com/skycoin/skywire/pkg/skyenv"
)

func TestNoServerAppsOnJS(t *testing.T) {
	apps := func() []appserver.AppConfig {
		return []appserver.AppConfig{
			{Name: skyenv.VPNServerName, AutoStart: true},
			{Name: skyenv.SkysocksName, AutoStart: true},
			{Name: skyenv.SkysocksClientName, AutoStart: true},
		}
	}

	js := apps()
	noServerAppsOn("js", js)
	if js[0].AutoStart || js[1].AutoStart || !js[2].AutoStart {
		t.Fatalf("js: want only the servers off, got %+v", js)
	}

	linux := apps()
	noServerAppsOn("linux", linux)
	for _, a := range linux {
		if !a.AutoStart {
			t.Fatalf("linux: %s turned off", a.Name)
		}
	}
}
