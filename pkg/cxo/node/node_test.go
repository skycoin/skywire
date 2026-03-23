package node

import (
	"testing"
	"time"

	"github.com/skycoin/skycoin/src/cipher"

	"github.com/skycoin/skywire/pkg/cxo/skyobject"
	"github.com/skycoin/skywire/pkg/cxo/skyobject/registry"
)

const TM time.Duration = 2000 * time.Millisecond

func getTestConfigNotListen(prefix string) (c *Config) {
	c = getTestConfig(prefix)
	c.TCP.Listen = ""
	c.UDP.Listen = ""
	return
}

func getTestNodeNotListen(prefix string) (n *Node) {
	var err error
	if n, err = NewNode(getTestConfigNotListen(prefix)); err != nil {
		panic(err)
	}
	return
}

func TestNode_ID(t *testing.T) {

	var n = getTestNodeNotListen("test")
	defer n.Close() //nolint:errcheck,gosec

	if n.ID() == (cipher.PubKey{}) {
		t.Error("blank node ID")
	}

}

func TestNode_Config(t *testing.T) {
	// (conf *Config)

	var (
		conf   = getTestConfigNotListen("test")
		n, err = NewNode(conf)
	)

	assertNil(t, err)
	defer n.Close() //nolint:errcheck,gosec

	assertTrue(t, n.Config() == conf, "wrong config")

	var sc = skyobject.NewConfig()
	sc.InMemoryDB = true

	var c *skyobject.Container
	c, err = skyobject.NewContainer(sc)
	assertNil(t, err)

	n, err = NewNodeContainer(conf, c)
	assertNil(t, err)
	defer n.Close() //nolint:errcheck,gosec

	if nc := n.Config(); nc != conf {
		t.Error("wrong node config")
	} else if nc == nil {
		t.Error("nil config")
	} else if nc.Config != sc {
		t.Error("wrong Container config")
	}

}

func TestNode_Container(t *testing.T) {
	// (c *skyobject.Container)

	var config = getTestConfigNotListen("test")

	var n, err = NewNode(config)

	if err != nil {
		t.Fatal(err)
	}
	defer n.Close() //nolint:errcheck,gosec

	if n.Container() == nil {
		t.Error("missing container")
	}

	var sc = skyobject.NewConfig()
	sc.InMemoryDB = true

	var c *skyobject.Container
	if c, err = skyobject.NewContainer(sc); err != nil {
		t.Fatal(err)
	}

	if n, err = NewNodeContainer(config, c); err != nil {
		t.Fatal(err)
	}
	defer n.Close() //nolint:errcheck,gosec

	if n.Container() != c {
		t.Error("wrong container")
	}

}

func assertTrue(t *testing.T, v bool, msg string) {
	t.Helper()
	if v == false {
		t.Fatal(msg)
	}
}

func assertIDs(t *testing.T, cs []*Conn, ids ...cipher.PubKey) {
	t.Helper()
	var im = make(map[cipher.PubKey]struct{})
	for _, pk := range ids {
		im[pk] = struct{}{}
	}
	if len(im) != len(cs) {
		t.Fatal("wrong length")
	}
	for _, c := range cs {
		if _, ok := im[c.PeerID()]; ok == false {
			t.Fatal("missing id")
		}
		delete(im, c.PeerID())
	}
	if len(im) != 0 {
		t.Fatal("wrong length")
	}
}

func onRootReceivedToChannel(
	chanBufferSize int, //nolint:unparam //                         :
) (
	channel chan *registry.Root, //                :
	callback func(*Conn, *registry.Root) error, // :
) {

	channel = make(chan *registry.Root, 1)

	callback = func(_ *Conn, r *registry.Root) (_ error) {
		channel <- r
		return
	}
	return
}

func TestNode_Publish(t *testing.T) {
	// (r *registry.Root)

	// broadcast to all subscribers

	// create listener (server, generator of root objects)

	var (
		ln = getTestNode("server")

		gr1, gr2, gn1 chan *registry.Root // sc1, sc2, uc1 (should not receive)

		sc1 = getTestConfigNotListen("sc1") // subscriber
		sc2 = getTestConfigNotListen("sc2") // subscriber

		uc1 = getTestConfigNotListen("uc1") // not subscribed
	)

	defer ln.Close() //nolint:errcheck,gosec

	// handle received roots

	gr1, sc1.OnRootReceived = onRootReceivedToChannel(1)
	gr2, sc2.OnRootReceived = onRootReceivedToChannel(1)
	gn1, uc1.OnRootReceived = onRootReceivedToChannel(1)

	// create clients (subscribed, subscribed, and not subscribed)

	var (
		sn1, sn2, un1 *Node
		err           error
	)

	sn1, err = NewNode(sc1)
	assertNil(t, err)
	defer sn1.Close() //nolint:errcheck,gosec

	sn2, err = NewNode(sc2)
	assertNil(t, err)
	defer sn2.Close() //nolint:errcheck,gosec

	un1, err = NewNode(uc1)
	assertNil(t, err)
	defer un1.Close() //nolint:errcheck,gosec

	// feed and owner

	var pk, sk = cipher.GenerateKeyPair() //nolint:errcheck,gosec

	assertNil(t, ln.Share(pk))

	// check out connections

	assertTrue(t, len(ln.Connections()) == 0, "unexpected connectionss")
	assertTrue(t, len(sn1.Connections()) == 0, "unexpected connectionss")
	assertTrue(t, len(sn2.Connections()) == 0, "unexpected connectionss")
	assertTrue(t, len(un1.Connections()) == 0, "unexpected connectionss")

	assertTrue(t, len(ln.ConnectionsOfFeed(pk)) == 0,
		"unexpected connectionss")
	assertTrue(t, len(sn1.ConnectionsOfFeed(pk)) == 0,
		"unexpected connectionss")
	assertTrue(t, len(sn2.ConnectionsOfFeed(pk)) == 0,
		"unexpected connectionss")
	assertTrue(t, len(un1.ConnectionsOfFeed(pk)) == 0,
		"unexpected connectionss")

	// conenct to the listener (and subscribe / not subscribe)

	// sn1
	var c *Conn
	c, err = sn1.TCP().Connect(ln.TCP().Address())
	assertNil(t, err)
	assertTrue(t, c.PeerID() == ln.ID(), "wrong ID")
	assertIDs(t, ln.Connections(), sn1.ID())

	assertNil(t, c.Subscribe(pk))
	assertIDs(t, ln.ConnectionsOfFeed(pk), sn1.ID())

	// sn2
	c, err = sn2.TCP().Connect(ln.TCP().Address())
	assertNil(t, err)
	assertTrue(t, c.PeerID() == ln.ID(), "wrong ID")
	assertIDs(t, ln.Connections(), sn1.ID(), sn2.ID())

	assertNil(t, c.Subscribe(pk))
	assertIDs(t, ln.ConnectionsOfFeed(pk), sn1.ID(), sn2.ID())

	// un1
	c, err = un1.TCP().Connect(ln.TCP().Address())
	assertNil(t, err)
	assertTrue(t, c.PeerID() == ln.ID(), "wrong id")

	// check all connections

	// ln
	assertIDs(t, ln.Connections(), sn1.ID(), sn2.ID(), un1.ID())
	assertIDs(t, ln.ConnectionsOfFeed(pk), sn1.ID(), sn2.ID())
	// sn1
	assertIDs(t, sn1.Connections(), ln.ID())
	assertIDs(t, sn1.ConnectionsOfFeed(pk), ln.ID())
	// sn2
	assertIDs(t, sn2.Connections(), ln.ID())
	assertIDs(t, sn2.ConnectionsOfFeed(pk), ln.ID())
	// un1
	assertIDs(t, un1.Connections(), ln.ID())
	assertIDs(t, un1.ConnectionsOfFeed(pk))

	// generate Root

	var up *skyobject.Unpack
	if up, err = ln.Container().Unpack(sk, getTestRegistry()); err != nil {
		t.Fatal(err)
	}

	var r = new(registry.Root)

	r.Pub = pk
	r.Nonce = 9012

	// save blank Root (it's enough for this test)

	if err = ln.Container().Save(up, r); err != nil {
		t.Error(err)
	}

	// publish

	ln.Publish(r)

	// check out channels

	var after = time.After(TM)

	select {
	case <-gr1:
		select {
		case <-gr2:
		case <-after:
			t.Fatal("not received")
		}
	case <-gr2:
		select {
		case <-gr1:
		case <-after:
			t.Fatal("not received")
		}
	case <-after:
		t.Fatal("not received")
	}

	select {
	case <-gn1:
		t.Fatal("received")
	case <-after:
	}

	// ok, now let's disconnect one by one and check
	// connections again

	// un1
	c, err = un1.TCP().Connect(ln.TCP().Address())
	assertNil(t, err)
	c.Close() //nolint:errcheck,gosec

	<-time.After(TM)

	assertIDs(t, ln.Connections(), sn1.ID(), sn2.ID())
	assertIDs(t, ln.ConnectionsOfFeed(pk), sn1.ID(), sn2.ID())
	assertIDs(t, un1.Connections())
	assertIDs(t, un1.ConnectionsOfFeed(pk))

	// sn2
	c, err = sn2.TCP().Connect(ln.TCP().Address())
	assertNil(t, err)
	c.Close() //nolint:errcheck,gosec

	<-time.After(TM)

	assertIDs(t, ln.Connections(), sn1.ID())
	assertIDs(t, ln.ConnectionsOfFeed(pk), sn1.ID())
	assertIDs(t, sn2.Connections())
	assertIDs(t, sn2.ConnectionsOfFeed(pk))

	// sn1
	c, err = sn1.TCP().Connect(ln.TCP().Address())
	assertNil(t, err)
	c.Close() //nolint:errcheck,gosec

	<-time.After(TM)

	assertIDs(t, ln.Connections())
	assertIDs(t, ln.ConnectionsOfFeed(pk))
	assertIDs(t, sn1.Connections())
	assertIDs(t, sn1.ConnectionsOfFeed(pk))

}

func TestNode_ConnectionsOfFeed(t *testing.T) {
	// (feed cipher.PubKey) (cs []*Conn)

	// moved to Publish

}

func TestNode_Connections(t *testing.T) {
	// (cs []*Conn)

	// moved to Publish

}

func TestNode_TCP(t *testing.T) {
	// (tcp *TCP)

	// TODO (kostyarin): low priority

}

func TestNode_UDP(t *testing.T) {
	// (udp *UDP)

	// TODO (kostyarin): low priority

}

func TestNode_Share(t *testing.T) {
	// (feed cipher.PubKey) (err error)

	var (
		n = getTestNodeNotListen("test")

		err error

		pk1, _ = cipher.GenerateKeyPair() //nolint:errcheck,gosec
		pk2, _ = cipher.GenerateKeyPair() //nolint:errcheck,gosec
		pk3, _ = cipher.GenerateKeyPair() //nolint:errcheck,gosec
	)

	defer n.Close() //nolint:errcheck,gosec

	if err = n.Container().AddFeed(pk1); err != nil {
		t.Fatal(err)
	}

	if len(n.Feeds()) != 0 {
		t.Fatal("share something, but should not")
	}

	if n.IsSharing(pk1) == true {
		t.Error("share, but should not")
	}

	if err = n.DontShare(pk1); err != nil {
		t.Fatal(err)
	}

	if n.IsSharing(pk1) == true {
		t.Error("share, but should not")
	}

	// add

	if err = n.Share(pk1); err != nil {
		t.Fatal(err)
	}

	if n.IsSharing(pk1) == false {
		t.Error("doesn't share, but should")
	}

	var feeds = n.Feeds()

	if len(feeds) != 1 {
		t.Fatal("wrong feeds length:", len(feeds))
	}

	if feeds[0] != pk1 {
		t.Fatal("wrong feed")
	}

	// del

	if err = n.DontShare(pk1); err != nil {
		t.Fatal(err)
	}

	if n.IsSharing(pk1) == true {
		t.Error("share, but should not")
	}

	if len(n.Feeds()) != 0 {
		t.Fatal("share something, but should not")
	}

	// add, add, add

	var fs = []cipher.PubKey{pk1, pk2, pk3}

	for _, pk := range fs {
		if err = n.Share(pk); err != nil {
			t.Fatal(err)
		}
	}

	for _, pk := range fs {
		if n.IsSharing(pk) == false {
			t.Error("doesn't share, but should")
		}
	}

	if feeds = n.Feeds(); len(feeds) != 3 {
		t.Fatal("wrong length:", len(feeds))
	}

	var fm = map[cipher.PubKey]struct{}{
		pk1: {},
		pk2: {},
		pk3: {},
	}

	for _, pk := range feeds {
		if _, ok := fm[pk]; ok == false {
			t.Fatal("missing feed")
		}
		delete(fm, pk)
	}

}

func TestNode_DontShare(t *testing.T) {
	// (feed cipher.PubKey) (err error)

	// moved to Share

}

func TestNode_Feeds(t *testing.T) {
	// (feeds []cipher.PubKey)

	// moved to Share

}

func TestNode_IsSharing(t *testing.T) {
	// (feed cipher.PubKey) (ok bool)

	// moved to Share

}

func TestNode_Stat(t *testing.T) {
	// (s *Stat)

	// TODO (kostyarin): the lowest priority

}

func TestNode_Close(t *testing.T) {
	// (err error)

	// TODO (kostyarin): the lowest priority

}
