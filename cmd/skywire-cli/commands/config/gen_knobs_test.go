package cliconfig

import (
	"testing"

	"github.com/spf13/pflag"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/app/appserver"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/proxyroute"
	"github.com/skycoin/skywire/pkg/visor/visorconfig"
)

// genOnlyFlags act on one run of config gen rather than set the visor, so
// they have no skywire.conf variable. sk is the visor's key, which a regen keeps.
var genOnlyFlags = map[string]bool{
	"stdout": true, "squash": true, "hide": true, "envs": true, "envout": true,
	"force": true, "regen": true, "retainhv": true, "nofetch": true, "nodefaults": true,
	"sn": true, "dmsgdisc": true, "dmsgsrv": true, "tpd": true, "sd": true, "ar": true, "rf": true,
	"noauth": true, "auth": true, "example-apps": true, "os": true, "sk": true, "all": true,
}

// TestEveryGenFlagHasAConfVariable keeps skywire.conf the source of truth. A
// setting with no variable is lost on the next autoconfig.
func TestEveryGenFlagHasAConfVariable(t *testing.T) {
	fs := genConfigCmd.Flags()
	fromConf := map[string]bool{}
	for _, d := range skyenvFlagDefaults[fs] {
		fromConf[d.name] = true
	}
	fs.VisitAll(func(f *pflag.Flag) {
		if !fromConf[f.Name] && !genOnlyFlags[f.Name] {
			t.Errorf("config gen --%s reads no skywire.conf variable", f.Name)
		}
	})
}

// TestRegenKeepsKeyringAndResolverRules. The keyring and the resolvers'
// per-domain rules are written by the visor and the CLI, not by config gen.
func TestRegenKeepsKeyringAndResolverRules(t *testing.T) {
	restoreRegen, restoreOld, restoreConf := isRegen, oldConfCache, conf
	restoreDW, restoreSW := enableDmsgWeb, enableSkynetWeb
	t.Cleanup(func() {
		isRegen, oldConfCache, conf = restoreRegen, restoreOld, restoreConf
		enableDmsgWeb, enableSkynetWeb = restoreDW, restoreSW
	})

	rules := []proxyroute.Rule{{Suffix: "example.com", Upstream: proxyroute.Direct}}
	isRegen = true
	oldConfCache = &visorconfig.V1{
		KeyRing:   &visorconfig.KeyRing{Type: "hd", NextIndex: 3},
		DmsgWeb:   &visorconfig.DmsgWebConfig{Enable: true, UpstreamRules: rules},
		SkynetWeb: &visorconfig.SkynetWebConfig{Enable: true, UpstreamRules: rules},
		Launcher:  &visorconfig.Launcher{Apps: []appserver.AppConfig{{Name: "skysocks-client"}}},
	}
	conf = new(visorconfig.V1)
	conf.Launcher = &visorconfig.Launcher{Apps: []appserver.AppConfig{{Name: "skysocks-client"}}}
	mergeExistingApps(logging.MustGetLogger("test"))
	require.Equal(t, oldConfCache.KeyRing, conf.KeyRing, "keyring must survive a regen")

	enableDmsgWeb, enableSkynetWeb = true, true
	configureResolvingProxies(logging.MustGetLogger("test"))
	require.Equal(t, rules, conf.DmsgWeb.UpstreamRules, "dmsg_web upstream_rules must survive a regen")
	require.Equal(t, rules, conf.SkynetWeb.UpstreamRules, "skynet_web upstream_rules must survive a regen")
}
