package visor

import (
	"context"
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
	"github.com/skycoin/skywire/pkg/transport"
	"github.com/skycoin/skywire/pkg/transport/network"
	tptypes "github.com/skycoin/skywire/pkg/transport/types"
	"github.com/skycoin/skywire/pkg/visor/visorconfig"
)

// feedVisor is a visor whose TPD is a mock holding the given transports.
func feedVisor(t *testing.T, entries ...transport.Entry) *Visor {
	t.Helper()
	dc := transport.NewDiscoveryMock()
	for i := range entries {
		require.NoError(t, dc.RegisterTransportsV3(context.Background(), "", &entries[i]))
	}
	tm, err := transport.NewManager(nil, nil, nil, &transport.ManagerConfig{DiscoveryClient: dc}, network.ClientFactory{})
	require.NoError(t, err)
	return &Visor{conf: &visorconfig.V1{Common: &visorconfig.Common{}}, tpM: tm}
}

func localLink(host, svc cipher.PubKey, label transport.Label) transport.Entry {
	return transport.MakeEntry(host, svc, tptypes.LOCAL, label)
}

// A registration rides the system feed only when TPD shows the service and
// TPD itself on one host, with the service's link labeled LabelSysFeed.
func TestReadsSystemFeed(t *testing.T) {
	host, _ := cipher.GenerateKeyPair()
	other, _ := cipher.GenerateKeyPair()
	tpd, _ := cipher.GenerateKeyPair()
	sd, _ := cipher.GenerateKeyPair()

	require.False(t, feedVisor(t).readsSystemFeed(tpd, sd), "no host in TPD")
	require.False(t, feedVisor(t, localLink(host, tpd, transport.LabelAutomatic),
		localLink(host, sd, transport.LabelAutomatic)).readsSystemFeed(tpd, sd), "the host does not deliver to it")
	require.False(t, feedVisor(t, localLink(host, tpd, transport.LabelAutomatic),
		localLink(other, sd, transport.LabelSysFeed)).readsSystemFeed(tpd, sd), "not in TPD's process")
	require.True(t, feedVisor(t, localLink(host, tpd, transport.LabelAutomatic),
		localLink(host, sd, transport.LabelSysFeed)).readsSystemFeed(tpd, sd))
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

// With a labeled link, the dmsg discovery entry goes onto the visor's system
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

	host, _ := cipher.GenerateKeyPair()
	log := logging.MustGetLogger("test")
	unlabeled := feedVisor(t, localLink(host, tpd, transport.LabelAutomatic), localLink(host, dmsgd, transport.LabelAutomatic))
	unlabeled.conf.Transport = &visorconfig.Transport{DiscoveryDmsg: "dmsg://" + tpd.Hex() + ":80"}
	unlabeled.setSystemCXOPub(pub)
	_, ok := unlabeled.systemFeedFor(sysfeed.DmsgDiscovery, dmsgd, log)
	require.False(t, ok, "an unlabeled link: the entry keeps its own feed")

	v := feedVisor(t, localLink(host, tpd, transport.LabelAutomatic), localLink(host, dmsgd, transport.LabelSysFeed))
	v.conf.Transport = &visorconfig.Transport{DiscoveryDmsg: "dmsg://" + tpd.Hex() + ":80"}
	v.setSystemCXOPub(pub)
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
