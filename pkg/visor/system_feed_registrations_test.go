package visor

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/cxo/sysfeed"
	"github.com/skycoin/skywire/pkg/cxo/treestore"
	dmsgdisc "github.com/skycoin/skywire/pkg/dmsg/disc"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/dmsg/dmsgtest"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/servicedisc"
	"github.com/skycoin/skywire/pkg/visor/visorconfig"
)

// A new visor moves a registration onto the system feed only when the
// deployment says that service runs in TPD's process and the visor uses the
// deployment's own TPD and service. Against an older deployment, one with no
// feed_ingest, it keeps every service's own feed.
func TestSystemFeedCarries(t *testing.T) {
	tpd, _ := cipher.GenerateKeyPair()
	sd, _ := cipher.GenerateKeyPair()
	other, _ := cipher.GenerateKeyPair()
	doc := func(ingest ...string) *visorconfig.Services {
		return &visorconfig.Services{
			TransportDiscoveryDmsg: "dmsg://" + tpd.Hex() + ":80",
			ServiceDiscoveryDmsg:   "dmsg://" + sd.Hex() + ":80",
			FeedIngest:             ingest,
		}
	}

	require.False(t, systemFeedCarries(nil, sysfeed.ServiceDiscovery, tpd, sd))
	require.False(t, systemFeedCarries(doc(), sysfeed.ServiceDiscovery, tpd, sd), "older deployment")
	require.True(t, systemFeedCarries(doc(sysfeed.ServiceDiscovery), sysfeed.ServiceDiscovery, tpd, sd))
	require.False(t, systemFeedCarries(doc(sysfeed.DmsgDiscovery), sysfeed.ServiceDiscovery, tpd, sd), "not listed")
	require.False(t, systemFeedCarries(doc(sysfeed.ServiceDiscovery), sysfeed.ServiceDiscovery, tpd, other), "own SD")
	require.False(t, systemFeedCarries(doc(sysfeed.ServiceDiscovery), sysfeed.ServiceDiscovery, other, sd), "own TPD")
	require.False(t, systemFeedCarries(doc("route_finder"), "route_finder", tpd, sd), "unknown service")

	// Only the clearnet-named field set, as an older services file has it.
	d := doc(sysfeed.ServiceDiscovery)
	d.ServiceDiscovery, d.ServiceDiscoveryDmsg = d.ServiceDiscoveryDmsg, ""
	require.True(t, systemFeedCarries(d, sysfeed.ServiceDiscovery, tpd, sd))
}

type recordedLeaves map[string][]byte

func (r recordedLeaves) Put(path string, value []byte) error { r[path] = value; return nil }
func (r recordedLeaves) Delete(path string) error            { delete(r, path); return nil }

// The SD mirror writes its batched leaf through whatever sink it is given, so
// on the system feed it lands under the SD prefix.
func TestSDEntryMirrorOnPrefixedSink(t *testing.T) {
	leaves := recordedLeaves{}
	m := newSDEntryMirror()
	m.setPublisher(prefixLeaves{leaves, sysfeed.SDPrefix}, nil)

	pk, _ := cipher.GenerateKeyPair()
	e := servicedisc.Service{Type: servicedisc.ServiceTypeVisor, Addr: servicedisc.NewSWAddr(pk, 0)}
	m.PutEntry(e)
	require.Contains(t, leaves, sysfeed.SDPrefix+sdRegLeafPath)
	m.DelEntry(e)
	require.NotContains(t, leaves, sysfeed.SDPrefix+sdRegLeafPath, "an empty set deletes the leaf")
}

// prefixLeaves is prefixedSink over a recorder instead of a publisher.
type prefixLeaves struct {
	r      recordedLeaves
	prefix string
}

func (p prefixLeaves) Put(path string, value []byte) error { return p.r.Put(p.prefix+path, value) }
func (p prefixLeaves) Delete(path string) error            { return p.r.Delete(p.prefix + path) }

// With feed_ingest set, the dmsg discovery entry goes onto the visor's system
// feed under the dmsgd prefix, and the keepalive is unhealthy until TPD has
// answered an announce and subscribed.
func TestSystemFeedForDmsgdEntry(t *testing.T) {
	env := dmsgtest.NewEnv(t, 30*time.Second)
	require.NoError(t, env.Startup(0, 1, 0, &dmsg.Config{MinSessions: 1}))
	t.Cleanup(env.Shutdown)

	pk, sk := cipher.GenerateKeyPair()
	tpd, _ := cipher.GenerateKeyPair()
	dmsgd, _ := cipher.GenerateKeyPair()
	c, err := env.NewClientWithKeys(pk, sk, &dmsg.Config{MinSessions: 1})
	require.NoError(t, err)
	pub, err := treestore.NewWithDMSG(c, sk, treestore.PubConfig{InMemoryDB: true, BatchWindow: 5 * time.Millisecond})
	require.NoError(t, err)
	t.Cleanup(func() { _ = pub.Close() }) //nolint:errcheck

	v := &Visor{conf: &visorconfig.V1{
		Transport: &visorconfig.Transport{DiscoveryDmsg: "dmsg://" + tpd.Hex() + ":80"},
	}}
	v.setSystemCXOPub(pub)
	v.deploySvcLast = &visorconfig.Services{
		TransportDiscoveryDmsg: "dmsg://" + tpd.Hex() + ":80",
		DmsgDiscoveryDmsg:      "dmsg://" + dmsgd.Hex() + ":80",
	}
	log := logging.MustGetLogger("test")
	_, ok := v.systemFeedFor(sysfeed.DmsgDiscovery, dmsgd, log)
	require.False(t, ok, "no feed_ingest: the entry keeps its own feed")

	v.deploySvcLast.FeedIngest = []string{sysfeed.DmsgDiscovery}
	route, ok := v.systemFeedFor(sysfeed.DmsgDiscovery, dmsgd, log)
	require.True(t, ok)
	entryPublishHook(route.sink, log)(&dmsgdisc.Entry{Static: pk, Version: "0.0.1"})
	got, ok := pub.Get(sysfeed.DmsgdPrefix + registrationEntryPath)
	require.True(t, ok)
	require.Contains(t, string(got), pk.Hex())
	healthy, _ := route.healthy()
	require.False(t, healthy, "TPD has not subscribed")
	require.Equal(t, []string{sysfeed.DmsgDiscovery}, v.CXOFeedStates()[0].Registrations)
}
