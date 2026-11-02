package cliconfig

import (
	"reflect"
	"testing"

	"github.com/skycoin/skywire/pkg/app/appserver"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/skyenv"
	"github.com/skycoin/skywire/pkg/visor/visorconfig"
)

// A regen keeps skychat's old args, so SKYCHATADDR has to replace the kept
// --addr or a changed address never reaches a visor that already has skychat.
func TestSkychatAddrReachesKeptArgs(t *testing.T) {
	restoreRegen, restoreOld, restoreConf, restoreAddr := isRegen, oldConfCache, conf, skychatAddr
	t.Cleanup(func() {
		isRegen, oldConfCache, conf, skychatAddr = restoreRegen, restoreOld, restoreConf, restoreAddr
	})
	kept := []string{"--addr", skyenv.SkychatAddr, "--pair-enable"}
	regen := func() []string {
		isRegen = true
		oldConfCache = &visorconfig.V1{Launcher: &visorconfig.Launcher{Apps: []appserver.AppConfig{
			{Name: skyenv.SkychatName, AutoStart: true, Args: kept},
		}}}
		conf = &visorconfig.V1{Launcher: &visorconfig.Launcher{Apps: []appserver.AppConfig{
			{Name: skyenv.SkychatName, Args: skychatInternalArgs(skychatAddr, true, false)},
		}}}
		mergeExistingApps(logging.MustGetLogger("test"))
		return conf.Launcher.Apps[0].Args
	}

	skychatAddr = "0.0.0.0:8001"
	if got, want := regen(), []string{"--addr", "0.0.0.0:8001", "--pair-enable"}; !reflect.DeepEqual(got, want) {
		t.Errorf("SKYCHATADDR set: args = %v, want %v", got, want)
	}

	// Unset, the kept args win, so an address set with `visor app args` stays.
	kept = []string{"--addr", "192.168.1.5:8001", "--pair-enable"}
	skychatAddr = skyenv.SkychatAddr
	if got := regen(); !reflect.DeepEqual(got, kept) {
		t.Errorf("SKYCHATADDR unset: args = %v, want the kept %v", got, kept)
	}
}
