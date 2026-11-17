// Package cliconfig cmd/skywire-cli/commands/config/gen_regen_autostart_test.go c4-vis-cli
package cliconfig

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/skycoin/skywire/pkg/app/appserver"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/skyenv"
	"github.com/skycoin/skywire/pkg/visor/visorconfig"
)

// TestRegenHonorsDeclaredAutostart: PROXYSERVER=false in skywire.conf has to
// turn off a skysocks that the old config autostarts (#6184), and the same for
// the other server toggles. A toggle the conf leaves out keeps the old value.
func TestRegenHonorsDeclaredAutostart(t *testing.T) {
	restoreRegen, restoreOld, restoreConf, restoreEnv := regenerate, oldConfCache, conf, skyenvfile
	restoreProxy, restoreVPN, restoreChat := proxyServerEnable, vpnServerEnable, skychatEnable
	t.Cleanup(func() {
		regenerate, oldConfCache, conf, skyenvfile = restoreRegen, restoreOld, restoreConf, restoreEnv
		proxyServerEnable, vpnServerEnable, skychatEnable = restoreProxy, restoreVPN, restoreChat
	})

	skyenvfile = filepath.Join(t.TempDir(), "skywire.conf")
	if err := os.WriteFile(skyenvfile, []byte("PROXYSERVER=false\nVPNSERVER=false\n"), 0600); err != nil {
		t.Fatal(err)
	}
	// What gen's flags resolve to from that file. SKYCHAT is not in it.
	proxyServerEnable, vpnServerEnable, skychatEnable = false, false, false

	regenerate = true
	oldConfCache = &visorconfig.V1{Launcher: &visorconfig.Launcher{Apps: []appserver.AppConfig{
		{Name: skyenv.SkysocksName, AutoStart: true},
		{Name: skyenv.VPNServerName, AutoStart: true},
		{Name: skyenv.SkychatName, AutoStart: true},
	}}}
	conf = new(visorconfig.V1)
	conf.Launcher = &visorconfig.Launcher{Apps: []appserver.AppConfig{
		{Name: skyenv.SkysocksName, AutoStart: proxyServerEnable},
		{Name: skyenv.VPNServerName, AutoStart: vpnServerEnable},
		{Name: skyenv.SkychatName, AutoStart: skychatEnable},
	}}
	mergeExistingApps(logging.MustGetLogger("test"))

	want := map[string]bool{skyenv.SkysocksName: false, skyenv.VPNServerName: false, skyenv.SkychatName: true}
	for _, a := range conf.Launcher.Apps {
		if a.AutoStart != want[a.Name] {
			t.Errorf("%s auto_start = %t after regen, want %t", a.Name, a.AutoStart, want[a.Name])
		}
	}
}
