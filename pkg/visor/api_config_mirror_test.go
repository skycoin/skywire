package visor

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	dmsgspec "github.com/skycoin/skywire/pkg/dmsg/dmsgc/spec"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/pty"
	"github.com/skycoin/skywire/pkg/skywireconfig/autoconfigcmd"
	"github.com/skycoin/skywire/pkg/visor/visorconfig"
)

// Every mirrored path must name a real config field and an autoconfig flag
// that writes skywire.conf, or the mirror silently drops it.
func TestAutoconfigFlagForIsValid(t *testing.T) {
	conf, _ := newTestConfig(t)
	// Gen writes the bridge block only when it is on; the mirror reads a
	// missing block as its zero value.
	conf.SkymailBridge = &visorconfig.SkymailBridgeConfig{}
	root := reflect.ValueOf(conf).Elem()
	for path, flagFor := range autoconfigFlagFor {
		tgt, err := resolveConfigTarget(root, path)
		require.NoError(t, err, path)
		flag, val := flagFor(reflect.Zero(tgt.typ))
		_, err = autoconfigcmd.EditFor(flag, val)
		require.NoError(t, err, path)
	}
}

func TestSetPtyWhitelistLiveAndMirrored(t *testing.T) {
	conf, _ := newTestConfig(t)
	envFile := filepath.Join(t.TempDir(), "skywire.conf")
	require.NoError(t, os.WriteFile(envFile, []byte("#DMSGPTYPKS=('')\n#VISORISPUBLIC=false\n"), 0o600))
	conf.Skyenv = envFile

	hv, _ := cipher.GenerateKeyPair()
	a, _ := cipher.GenerateKeyPair()
	b, _ := cipher.GenerateKeyPair()
	conf.Hypervisors = []cipher.PubKey{hv}
	conf.Pty.Whitelist = []cipher.PubKey{hv, a}
	wl := pty.NewMemoryWhitelist()
	require.NoError(t, wl.Add(conf.PK, hv, a))
	v := &Visor{conf: conf, peerWhitelist: wl, log: logging.MustGetLogger("test")}
	v.watchSkyenv()

	changes, err := v.SetConfigFields(map[string]json.RawMessage{
		"pty.whitelist": json.RawMessage(`["` + b.String() + `"]`),
		"is_public":     json.RawMessage(`true`),
	})
	require.NoError(t, err)
	require.True(t, changes[1].Live, changes[1].Path)

	ok, _ := wl.Get(b) //nolint:errcheck
	require.True(t, ok, "an added key is admitted at once")
	ok, _ = wl.Get(a) //nolint:errcheck
	require.False(t, ok, "a removed key loses trust")
	ok, _ = wl.Get(hv) //nolint:errcheck
	require.True(t, ok, "a hypervisor keeps its trust")
	require.Equal(t, []cipher.PubKey{b}, conf.Pty.Whitelist)

	env, err := os.ReadFile(envFile) //nolint:gosec
	require.NoError(t, err)
	require.Contains(t, string(env), "DMSGPTYPKS=('"+b.String()+"')")
	require.True(t, strings.Contains(string(env), "VISORISPUBLIC=true"), string(env))
}

// A config that records no skywire.conf was not generated from one, so a live
// change leaves every skywire.conf alone.
func TestNoSkyenvRecordedWritesNothing(t *testing.T) {
	conf, _ := newTestConfig(t)
	envFile := filepath.Join(t.TempDir(), "skywire.conf")
	require.NoError(t, os.WriteFile(envFile, []byte("#VISORISPUBLIC=false\n"), 0o600))
	conf.Skyenv = ""
	v := &Visor{conf: conf, log: logging.MustGetLogger("test")}
	v.watchSkyenv()

	_, err := v.SetConfigFields(map[string]json.RawMessage{"is_public": json.RawMessage(`true`)})
	require.NoError(t, err)
	env, err := os.ReadFile(envFile) //nolint:gosec
	require.NoError(t, err)
	require.Equal(t, "#VISORISPUBLIC=false\n", string(env))
}

// skyenv names the file the visor writes, so only config gen sets it.
func TestSkyenvIsNotSettable(t *testing.T) {
	conf, _ := newTestConfig(t)
	v := &Visor{conf: conf, log: logging.MustGetLogger("test")}
	_, err := v.SetConfigFields(map[string]json.RawMessage{"skyenv": json.RawMessage(`"/etc/passwd"`)})
	require.ErrorContains(t, err, "skywire autoconfig")
	require.Empty(t, conf.Skyenv)
}

// regenKeeps are live settings that config gen carries over from the old
// config on a regenerate instead of reading a skywire.conf variable.
var regenKeeps = map[string]string{
	"launcher.apps[*].auto_start":        "mergeExistingApps",
	"launcher.apps[*].args":              "mergeExistingApps",
	"launcher.apps[*].env":               "mergeExistingApps",
	"routing.router_settings[*]":         "configureRouting",
	"routing.transport_preference":       "configureRouting",
	"dmsg_web.upstream_rules":            "configureResolvingProxies",
	"skynet_web.upstream_rules":          "configureResolvingProxies",
	"persistent_transports":              "keepPersistentTransports",
	"transport.autoconnect.site_links":   "keepAutoconnect",
	"transport.autoconnect.site_types":   "keepAutoconnect",
	"transport.autoconnect.max_per_type": "keepAutoconnect",
	"transport.autoconnect.max_total":    "keepAutoconnect",
	"transport.tcp_notsent_lowat":        "keepTCPNotSentLowat",
	"transport.network_watch_interval":   "keepNetworkWatch",
	"transport.public_ip_check_interval": "keepNetworkWatch",
}

// TestEveryLiveSettingSurvivesAutoconfig. A live change that neither reaches
// skywire.conf nor is carried over is undone by the next autoconfig.
func TestEveryLiveSettingSurvivesAutoconfig(t *testing.T) {
	for _, f := range liveConfigFieldTable {
		_, mirrored := autoconfigFlagFor[f.Path]
		_, kept := regenKeeps[f.Path]
		if !mirrored && !kept {
			t.Errorf("%s is live but neither mirrored to skywire.conf nor kept by config gen", f.Path)
		}
	}
}

// A setter that persists without going through config set is saved to
// skywire.conf too, since the save runs on every config write.
func TestFlushMirrorsAnyWriter(t *testing.T) {
	conf, _ := newTestConfig(t)
	envFile := filepath.Join(t.TempDir(), "skywire.conf")
	require.NoError(t, os.WriteFile(envFile, []byte("#MINHOPS=0\n"), 0o600))
	conf.Skyenv = envFile
	if conf.Routing == nil {
		conf.Routing = &visorconfig.Routing{}
	}
	v := &Visor{conf: conf, log: logging.MustGetLogger("test")}
	v.watchSkyenv()

	require.NoError(t, conf.UpdateMinHops(3))
	env, err := os.ReadFile(envFile) //nolint:gosec
	require.NoError(t, err)
	require.Contains(t, string(env), "MINHOPS=3")
}

// Removing a block saves its settings as their zero values, so a regenerate
// does not bring the block back.
func TestFlushMirrorsARemovedBlock(t *testing.T) {
	conf, _ := newTestConfig(t)
	envFile := filepath.Join(t.TempDir(), "skywire.conf")
	require.NoError(t, os.WriteFile(envFile, []byte("DMSGWEB=true\n"), 0o600))
	conf.Skyenv = envFile
	conf.DmsgWeb = &visorconfig.DmsgWebConfig{Enable: true}
	v := &Visor{conf: conf, log: logging.MustGetLogger("test")}
	v.watchSkyenv()

	conf.DmsgWeb = nil
	require.NoError(t, conf.Flush())
	env, err := os.ReadFile(envFile) //nolint:gosec
	require.NoError(t, err)
	require.Contains(t, string(env), "DMSGWEB=false")
}

// sessions_count lives in the primary deployment once a config is loaded.
// Setting only the top-level copy was never written, and the old value came
// back at the next start. The change is also saved to MINDMSGSESS.
func TestSessionsCountIsWrittenAndMirrored(t *testing.T) {
	conf, path := newTestConfig(t)
	envFile := filepath.Join(t.TempDir(), "skywire.conf")
	require.NoError(t, os.WriteFile(envFile, []byte("#MINDMSGSESS=2\n"), 0o600))
	conf.Skyenv = envFile
	conf.Dmsg = new(dmsgspec.DmsgConfig)
	require.NoError(t, json.Unmarshal([]byte(`{"discovery":"http://d","sessions_count":2}`), conf.Dmsg))
	require.Len(t, conf.Dmsg.Deployments, 1)
	v := &Visor{conf: conf, log: logging.MustGetLogger("test")}
	v.watchSkyenv()

	setSessionsCount(conf.Dmsg, 5)
	require.NoError(t, conf.Flush())

	written, err := os.ReadFile(path) //nolint:gosec
	require.NoError(t, err)
	require.Contains(t, string(written), `"sessions_count": 5`)
	env, err := os.ReadFile(envFile) //nolint:gosec
	require.NoError(t, err)
	require.Contains(t, string(env), "MINDMSGSESS=5")
}

// The autoconnect limits and memory_limit are saved to their variables, and
// an unset site_links comments its variable out instead of writing one.
func TestAutoconnectLimitsAreMirrored(t *testing.T) {
	conf, _ := newTestConfig(t)
	envFile := filepath.Join(t.TempDir(), "skywire.conf")
	require.NoError(t, os.WriteFile(envFile, []byte("#MEMORYLIMIT=none\n#AUTOCONNMAXTOTAL=0\n#AUTOCONNSITELINKS=2\n#AUTOCONNMAXPERTYPE=''\n"), 0o600))
	conf.Skyenv = envFile
	v := &Visor{conf: conf, log: logging.MustGetLogger("test")}
	v.watchSkyenv()

	three := 3
	require.NoError(t, conf.UpdateAutoconnect(func(a *visorconfig.Autoconnect) {
		a.MaxTotal = 80
		a.SiteLinks = &three
		a.MaxPerType = map[string]int{"webrtc": 10, "sudph": 40}
	}))
	conf.MemoryLimit = "256MiB"
	require.NoError(t, conf.Flush())
	env, err := os.ReadFile(envFile) //nolint:gosec
	require.NoError(t, err)
	for _, want := range []string{"MEMORYLIMIT='256MiB'", "AUTOCONNMAXTOTAL=80", "AUTOCONNSITELINKS=3", "AUTOCONNMAXPERTYPE='sudph=40,webrtc=10'"} {
		require.Contains(t, string(env), want)
	}

	require.NoError(t, conf.UpdateAutoconnect(func(a *visorconfig.Autoconnect) { a.SiteLinks = nil }))
	env, err = os.ReadFile(envFile) //nolint:gosec
	require.NoError(t, err)
	require.NotContains(t, string(env), "\nAUTOCONNSITELINKS=")
}

// Turning the mail bridge on or off is written to SKYMAILBRIDGE, so the next
// autoconfig keeps it. Before, `mail up` set it only until the next restart.
func TestBridgeToggleReachesSkyenv(t *testing.T) {
	conf, _ := newTestConfig(t)
	envFile := filepath.Join(t.TempDir(), "skywire.conf")
	require.NoError(t, os.WriteFile(envFile, []byte("#SKYMAILBRIDGE=false\n"), 0o600))
	conf.Skyenv = envFile
	v := &Visor{conf: conf, log: logging.MustGetLogger("test"), initLock: new(sync.RWMutex)}
	v.watchSkyenv()

	// Off needs no dmsg client, so the toggle itself can run here.
	conf.SkymailBridge = &visorconfig.SkymailBridgeConfig{Enable: true}
	require.NoError(t, conf.Flush())
	env, err := os.ReadFile(envFile) //nolint:gosec
	require.NoError(t, err)
	require.Contains(t, string(env), "SKYMAILBRIDGE=true")

	require.NoError(t, v.SetEmbeddedProxyEnabled("bridge", false))
	require.False(t, conf.SkymailBridge.Enable)
	env, err = os.ReadFile(envFile) //nolint:gosec
	require.NoError(t, err)
	require.Contains(t, string(env), "SKYMAILBRIDGE=false")
}
