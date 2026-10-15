package cliconfig

import (
	"os"
	"reflect"
	"testing"

	"github.com/skycoin/skywire/pkg/app/appserver"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/skyenv"
	"github.com/skycoin/skywire/pkg/visor/visorconfig"
)

// TestSkyDNSKnobs: SKYDNS and RESOLVERCA have to reach a config that already
// has a skydns entry, since a regen otherwise keeps the old autostart and args.
func TestSkyDNSKnobs(t *testing.T) {
	restoreRegen, restoreOld, restoreConf := isRegen, oldConfCache, conf
	restoreOn, restoreCA := isSkyDNSEnable, resolverCA
	t.Cleanup(func() {
		isRegen, oldConfCache, conf = restoreRegen, restoreOld, restoreConf
		isSkyDNSEnable, resolverCA = restoreOn, restoreCA
	})
	isSkyDNSEnable, resolverCA = true, "/root/.skywire/resolver"

	if got := skydnsArgs([]string{"app", "skydns"}); !reflect.DeepEqual(got, []string{"app", "skydns", "--ca", "/root/.skywire/resolver"}) {
		t.Errorf("fresh skydns args = %v", got)
	}

	isRegen = true
	oldConfCache = &visorconfig.V1{Launcher: &visorconfig.Launcher{Apps: []appserver.AppConfig{
		{Name: skyenv.SkyDNSName, AutoStart: false, Args: []string{"app", "skydns", "--dns", "9.9.9.9"}},
	}}}
	conf = new(visorconfig.V1)
	conf.Launcher = &visorconfig.Launcher{Apps: []appserver.AppConfig{
		{Name: skyenv.SkyDNSName, AutoStart: isSkyDNSEnable, Args: skydnsArgs([]string{"app", "skydns"})},
	}}
	mergeExistingApps(logging.MustGetLogger("test"))

	a := conf.Launcher.Apps[0]
	if !a.AutoStart {
		t.Error("SKYDNS=true must turn on autostart over a kept false")
	}
	if want := []string{"app", "skydns", "--dns", "9.9.9.9", "--ca", "/root/.skywire/resolver"}; !reflect.DeepEqual(a.Args, want) {
		t.Errorf("regen args = %v, want the kept --dns plus --ca: %v", a.Args, want)
	}

	if got, ok := FlagArgsForSkyenv("SKYDNS", "true"); !ok || !reflect.DeepEqual(got, []string{"--skydns=true"}) {
		t.Errorf("FlagArgsForSkyenv(SKYDNS) = %v, %v", got, ok)
	}
}

// skydns only defaults on for a config made as root, as the packages make it.
func TestSkyDNSDefaultNeedsRoot(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root")
	}
	if skyDNSDefault() {
		t.Error("skydns must not default on for a config made without root")
	}
}
