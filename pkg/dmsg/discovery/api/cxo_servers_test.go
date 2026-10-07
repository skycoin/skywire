package api

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/cxo/node"
	"github.com/skycoin/skywire/pkg/cxo/treestore"
	"github.com/skycoin/skywire/pkg/dmsg/disc"
	"github.com/skycoin/skywire/pkg/dmsg/discovery/serverfeed"
	"github.com/skycoin/skywire/pkg/logging"
)

func newRunningPublisher(t *testing.T) *ClientsByServerCXOPublisher {
	t.Helper()
	cfg := node.NewConfig()
	cfg.TCP.Listen, cfg.UDP.Listen, cfg.RPC = "", "", ""
	cfg.Config.InMemoryDB = true
	n, err := node.NewNode(cfg)
	require.NoError(t, err)
	_, sk := cipher.GenerateKeyPair()
	pub, err := treestore.New(n, sk, treestore.Config{BatchWindow: 5 * time.Millisecond})
	require.NoError(t, err)
	p := &ClientsByServerCXOPublisher{
		pub: pub, log: logging.MustGetLogger("test"),
		events: make(chan func(), 16), done: make(chan struct{}),
		state:        make(map[cipher.PubKey]map[cipher.PubKey][]byte),
		servers:      make(map[cipher.PubKey]string),
		pendingDirty: make(map[cipher.PubKey]struct{}),
	}
	p.wg.Add(1)
	go p.run()
	t.Cleanup(func() { _ = p.Close(); _ = n.Close() }) //nolint:errcheck
	return p
}

func (p *ClientsByServerCXOPublisher) settle() {
	done := make(chan struct{})
	p.events <- func() { close(done) }
	<-done
}

func leaf(t *testing.T, p *ClientsByServerCXOPublisher, pk cipher.PubKey) *disc.Entry {
	t.Helper()
	body, ok := p.pub.Get(serverLeafPath(pk))
	if !ok {
		return nil
	}
	e := serverfeed.Decode(body)
	require.NotNil(t, e)
	return e
}

func TestPublishServers(t *testing.T) {
	p := newRunningPublisher(t)
	a, _ := cipher.GenerateKeyPair()
	b, _ := cipher.GenerateKeyPair()
	srv := func(pk cipher.PubKey, addr string, free int, ts int64) *disc.Entry {
		return &disc.Entry{Static: pk, Timestamp: ts, Sequence: uint64(ts), Server: &disc.Server{Address: addr, AvailableSessions: free}} //nolint:gosec
	}

	p.PublishServers([]*disc.Entry{srv(a, "1.1.1.1:80", 100, 1), srv(b, "2.2.2.2:80", 5, 1)})
	p.settle()
	require.Equal(t, "1.1.1.1:80", leaf(t, p, a).Server.Address)
	require.NotNil(t, leaf(t, p, b))

	p.PublishServers([]*disc.Entry{srv(a, "1.1.1.1:80", 42, 2), srv(b, "2.2.2.2:80", 5, 2)})
	p.settle()
	require.Equal(t, 100, leaf(t, p, a).Server.AvailableSessions, "a refresh that moves only sessions and timestamp is not republished")

	p.PublishServers([]*disc.Entry{srv(a, "9.9.9.9:80", 42, 3)})
	p.settle()
	require.Equal(t, "9.9.9.9:80", leaf(t, p, a).Server.Address, "an address change is republished")
	require.Nil(t, leaf(t, p, b), "a server that left is deleted")
}

func TestServerLeafIsIgnoredByBatchReaders(t *testing.T) {
	pk, _ := cipher.GenerateKeyPair()
	path := serverLeafPath(pk)
	require.Equal(t, clientsByServerPrefix(), path[:len("clients-by-server/")])
	require.NotContains(t, path, "/entry")
	require.Nil(t, serverfeed.Decode(encodeClientsBatch(nil)), "a batch leaf does not decode as a server")
}

func clientsByServerPrefix() string { return "clients-by-server/" }
