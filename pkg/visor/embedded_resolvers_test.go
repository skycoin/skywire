package visor

import (
	"context"
	"net"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/app/appserver"
	"github.com/skycoin/skywire/pkg/app/launcher"
	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/direct"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/visor/visorconfig"
)

// resolverTestVisor builds the minimum Visor initEmbeddedResolvers reads: a
// config, a dmsg client to hand the dmsg-kind runtimes, and the service
// registry SelfDial comes from. No network is touched — the dmsg client is
// only stored, never served.
func resolverTestVisor(t *testing.T, resolvers []visorconfig.ResolverConfig) *Visor {
	t.Helper()
	_, sk := cipher.GenerateKeyPair()
	common, err := visorconfig.NewCommon(nil, filepath.Join(t.TempDir(), "skywire-config.json"), &sk)
	require.NoError(t, err)
	conf := &visorconfig.V1{
		Common:    common,
		DmsgWeb:   &visorconfig.DmsgWebConfig{Enable: true},
		SkynetWeb: &visorconfig.SkynetWebConfig{Enable: true},
		Resolvers: resolvers,
	}
	log := conf.MasterLogger().PackageLogger("test")
	dc := dmsg.NewClient(conf.PK, sk, direct.NewClient(nil, log), &dmsg.Config{MinSessions: 0})
	return &Visor{conf: conf, dmsgC: dc, services: NewServiceRegistry(), initLock: new(sync.RWMutex)}
}

// TestInitEmbeddedResolvers_StartsN is the end-to-end config→runtime check:
// N entries in `resolvers` become N constructed runtimes, each bound to its own
// listener, each registered as its own launcher app row.
func TestInitEmbeddedResolvers_StartsN(t *testing.T) {
	v := resolverTestVisor(t, []visorconfig.ResolverConfig{
		{Name: "one", Enable: true, ProxyPort: 24447, ProxyAddr: "0.0.0.0"},
		{Name: "two", Enable: true, ProxyPort: 24448, DomainSuffix: ".alt"},
		{Name: "off", ProxyPort: 24449},
	})
	log := v.conf.MasterLogger().PackageLogger("test")
	require.NoError(t, initEmbeddedResolvers(context.Background(), v, log))

	require.Len(t, v.embeddedResolvers, 3, "every entry, enabled or not, is constructed")
	for _, name := range []string{"resolver-one", "resolver-two", "resolver-off"} {
		_, ok := launcher.GetApp(name)
		require.Truef(t, ok, "launcher app %q not registered", name)
	}

	// Each runtime carries its OWN listener + suffix — the failure this guards
	// is a shared config pointer, which would give every resolver the last
	// entry's port.
	require.Equal(t, "0.0.0.0:24447", v.embeddedResolvers[0].dmsg.ListenAddr())
	require.Equal(t, "127.0.0.1:24448", v.embeddedResolvers[1].dmsg.ListenAddr())
	require.Equal(t, ".alt", v.embeddedResolvers[1].dmsg.Suffix())
	require.Equal(t, ".dmsg", v.embeddedResolvers[0].dmsg.Suffix())

	// initLauncher turns the constructed set into app rows whose AutoStart
	// mirrors each entry's enable flag.
	apps := resolverAppRows(v)
	require.Equal(t, map[string]bool{
		"resolver-one": true, "resolver-two": true, "resolver-off": false,
	}, apps)
}

// resolverAppRows mirrors the loop initLauncher runs over v.embeddedResolvers,
// so the autostart wiring is testable without standing up a full launcher.
func resolverAppRows(v *Visor) map[string]bool {
	out := map[string]bool{}
	var apps []appserver.AppConfig
	for _, er := range v.embeddedResolvers {
		if appsContains(apps, er.appName) {
			continue
		}
		apps = append(apps, appserver.AppConfig{Name: er.appName, AutoStart: er.enable})
	}
	for _, a := range apps {
		out[a.Name] = a.AutoStart
	}
	return out
}

// TestInitEmbeddedResolvers_PortConflictConstructsNothing pins the loud-failure
// requirement: a set where two enabled resolvers claim one port yields NO
// resolvers at all, rather than a silent winner and a listener that never binds.
func TestInitEmbeddedResolvers_PortConflictConstructsNothing(t *testing.T) {
	v := resolverTestVisor(t, []visorconfig.ResolverConfig{
		{Name: "a", Enable: true, ProxyPort: 24447},
		{Name: "b", Enable: true, ProxyPort: 24447},
	})
	log := v.conf.MasterLogger().PackageLogger("test")
	require.NoError(t, initEmbeddedResolvers(context.Background(), v, log))
	require.Empty(t, v.embeddedResolvers)
}

// TestInitEmbeddedResolvers_ConflictWithPrimary: the extra resolver landing on
// dmsg_web's DEFAULT port is the conflict a config never spells out, so it is
// the one most likely to be missed.
func TestInitEmbeddedResolvers_ConflictWithPrimary(t *testing.T) {
	v := resolverTestVisor(t, []visorconfig.ResolverConfig{
		{Name: "a", Enable: true, ProxyPort: visorconfig.DefaultDmsgWebProxyPort},
	})
	log := v.conf.MasterLogger().PackageLogger("test")
	require.NoError(t, initEmbeddedResolvers(context.Background(), v, log))
	require.Empty(t, v.embeddedResolvers)
}

// TestInitEmbeddedResolvers_Chaining covers the chain knob both ways: chained
// (the default) inherits the same skynetweb upstream the primary dmsgweb gets;
// chain:false leaves the resolver unchained, which is what keeps a LAN-facing
// proxy from becoming an open clearnet relay.
func TestInitEmbeddedResolvers_Chaining(t *testing.T) {
	v := resolverTestVisor(t, []visorconfig.ResolverConfig{
		{Name: "chained", Enable: true, ProxyPort: 24447},
		{Name: "lan", Enable: true, ProxyPort: 24448, ProxyAddr: "0.0.0.0", Chain: new(bool)},
		{Name: "explicit", Enable: true, ProxyPort: 24449, UpstreamSOCKS: "127.0.0.1:9999"},
	})
	log := v.conf.MasterLogger().PackageLogger("test")
	require.NoError(t, initEmbeddedResolvers(context.Background(), v, log))
	require.Len(t, v.embeddedResolvers, 3)
	require.Equal(t, "127.0.0.1:4446", v.embeddedResolvers[0].dmsg.Upstream())
	require.Equal(t, "", v.embeddedResolvers[1].dmsg.Upstream())
	require.Equal(t, "127.0.0.1:9999", v.embeddedResolvers[2].dmsg.Upstream())
}

// TestInitEmbeddedResolvers_SkynetNeedsRouter: a skynet entry on a visor with
// no router is skipped with a warning rather than nil-panicking in the dialer.
func TestInitEmbeddedResolvers_SkynetNeedsRouter(t *testing.T) {
	v := resolverTestVisor(t, []visorconfig.ResolverConfig{
		{Name: "sky", Kind: visorconfig.ResolverKindSkynet, Enable: true, ProxyPort: 24450},
	})
	log := v.conf.MasterLogger().PackageLogger("test")
	require.NoError(t, initEmbeddedResolvers(context.Background(), v, log))
	require.Empty(t, v.embeddedResolvers)
}

// TestInitEmbeddedResolvers_Absent: the backward-compatible case. No
// `resolvers` key means no work and no resolvers — exactly today's behavior.
func TestInitEmbeddedResolvers_Absent(t *testing.T) {
	v := resolverTestVisor(t, nil)
	log := v.conf.MasterLogger().PackageLogger("test")
	require.NoError(t, initEmbeddedResolvers(context.Background(), v, log))
	require.Nil(t, v.embeddedResolvers)
}

// TestInitEmbeddedResolvers_Bind is the claim that matters operationally: N
// configured resolvers means N SOCKS5 listeners actually accepting on N ports.
// The runtimes are started directly (the launcher's AutoStart pass is what does
// this in a live visor) and both listeners are dialed.
//
// Skipped under -short: serve() waits on the dmsg client's readiness before
// binding, and this Visor's client never becomes ready, so the bind lands on
// the far side of that bounded wait.
func TestInitEmbeddedResolvers_Bind(t *testing.T) {
	if testing.Short() {
		t.Skip("bounded dmsg-readiness wait makes this slow")
	}
	v := resolverTestVisor(t, []visorconfig.ResolverConfig{
		{Name: "b1", Enable: true, ProxyPort: 24457},
		{Name: "b2", Enable: true, ProxyPort: 24458},
	})
	log := v.conf.MasterLogger().PackageLogger("test")
	require.NoError(t, initEmbeddedResolvers(context.Background(), v, log))
	require.Len(t, v.embeddedResolvers, 2)
	for _, er := range v.embeddedResolvers {
		require.NoError(t, er.dmsg.Start())
		defer er.dmsg.Stop() //nolint:errcheck
	}
	for _, addr := range []string{"127.0.0.1:24457", "127.0.0.1:24458"} {
		require.Eventually(t, func() bool {
			c, err := net.DialTimeout("tcp", addr, time.Second)
			if err != nil {
				return false
			}
			_ = c.Close() //nolint:errcheck
			return true
		}, 40*time.Second, 200*time.Millisecond, "resolver never bound %s", addr)
	}
}
