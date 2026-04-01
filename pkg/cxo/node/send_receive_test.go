package node

import (
	"fmt"
	"runtime"
	"testing"
	"time"

	"github.com/skycoin/skycoin/src/cipher"
	"github.com/skycoin/skycoin/src/cipher/encoder"

	"github.com/skycoin/skywire/pkg/cxo/skyobject"
	"github.com/skycoin/skywire/pkg/cxo/skyobject/registry"
)

type User struct {
	Name   string
	Age    uint32
	Hidden []byte `enc:"-"`
}

type Feed struct {
	Posts registry.Refs `skyobject:"schema=test.Post"`
}

type Post struct {
	Head string
	Body string
	Time int64
}

func getTestConfig(prefix string) (c *Config) {

	c = NewConfig()
	c.Logger.Prefix = "[" + prefix + "] "
	c.Config.InMemoryDB = true // use in-memory DB

	c.TCP.Listen = "127.0.0.1:0"
	c.TCP.ResponseTimeout = 1 * time.Second //
	c.TCP.Pings = 0                         // no pings
	c.RPC = ""                              // no rpc

	c.UDP.Listen = ""
	c.UDP.ResponseTimeout = 1 * time.Second //
	c.UDP.Pings = 0                         // no pings
	c.RPC = ""                              // no rpc

	if testing.Verbose() == true {
		c.Logger.Debug = true
		c.Logger.Pins = ^c.Logger.Pins // all
	}

	return

}

func getTestNode(prefix string) (n *Node) {

	var err error
	if n, err = NewNode(getTestConfig(prefix)); err != nil {
		panic(err)
	}

	return
}

func getTestRegistry() (r *registry.Registry) {

	return registry.NewRegistry(func(r *registry.Reg) {
		r.Register("test.User", User{})
		r.Register("test.Post", Post{})
		r.Register("test.Feed", Feed{})
	})

}

func assertNil(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func onRootFilledToChannel(
	chanBufferSize int, //nolint:unparam //                   :
) (
	channel chan *registry.Root, //          :
	callback func(*Node, *registry.Root), // :
) {

	channel = make(chan *registry.Root, 1)

	callback = func(_ *Node, r *registry.Root) {
		channel <- r
		return //nolint:staticcheck
	}
	return
}

func onFillingBreaksTestLog(
	t *testing.T, //                               :
) (
	ofbtl func(*Node, *registry.Root, error), // :
) {

	ofbtl = func(n *Node, r *registry.Root, err error) {
		t.Logf("filling of %s breaks by %v", r.Short(), err)
	}
	return
}

func Test_send_receive(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("CXO root filling is unreliable on Windows CI runners")
	}

	var (
		fr, onRootFilled   = onRootFilledToChannel(100)
		rr, onRootReceived = onRootReceivedToChannel(1)
		sn                 = getTestNode("sender")
		rconf              = getTestConfig("receiver")
	)

	rconf.TCP.Listen, rconf.UDP.Listen = "", ""       // don't listen
	rconf.OnRootFilled = onRootFilled                 // callback
	rconf.OnRootReceived = onRootReceived             // callback
	rconf.OnFillingBreaks = onFillingBreaksTestLog(t) // log

	var rn, err = NewNode(rconf)

	if err != nil {
		t.Fatal(err)
	}

	defer sn.Close() //nolint:errcheck,gosec
	defer rn.Close() //nolint:errcheck,gosec

	var pk, sk = cipher.GenerateKeyPair() //nolint:errcheck,gosec

	assertNil(t, sn.Share(pk))
	assertNil(t, rn.Share(pk))

	var (
		reg = getTestRegistry()
		sc  = sn.Container()

		up *skyobject.Unpack
	)

	if up, err = sc.Unpack(sk, reg); err != nil {
		t.Fatal(err)
	}

	var r = new(registry.Root)

	r.Nonce = 9021 // random
	r.Pub = pk     // set
	r.Descriptor = []byte("hey-ho!")

	r.Refs = append(r.Refs,
		dynamicByValue(t, up, "test.User", User{"Alice", 19, nil}),
		dynamicByValue(t, up, "test.Feed", Feed{}),
	)

	// save the Root
	if err := sc.Save(up, r); err != nil {
		t.Fatal(err)
	}

	if sn.TCP().Address() == "" {
		t.Fatal("blank listening address")
	}

	// connect the nodes (synchronous from client side; wait for server to register)
	var c *Conn
	if c, err = rn.TCP().Connect(sn.TCP().Address()); err != nil {
		t.Fatal(err)
	}

	waitForCondition(t, "sender registered connection", func() bool {
		return len(sn.Connections()) > 0
	})

	// subscribe and verify root reception with retry.
	// On slow CI runners the sender's receiveMsg goroutine may not be
	// scheduled yet, causing the root push after Subscribe to be lost.
	subscribeWithRetry(t, c, pk, rr, 3)

	// wait for the root to be filled on the receiver
	select {
	case <-fr:
	case <-time.After(30 * time.Second):
		t.Log("Root :    ", r.Hash.Hex()[:7])
		t.Log("Registry: ", r.Reg.Short())

		t.Log("[sender] objects")
		printObjects(t, "sender", sc)

		t.Log("[receiver] objects")
		printObjects(t, "receiver", rn.Container())

		t.Fatal("timed out waiting for root replication")
	}
}

// subscribeWithRetry subscribes to a feed and waits for the root to be received.
// If the root doesn't arrive within TM, it unsubscribes and retries up to maxRetries.
// This handles the race where the sender's receiveMsg goroutine isn't scheduled
// when the subscription response arrives, causing sendLastRoot to be lost.
func subscribeWithRetry(t *testing.T, c *Conn, feed cipher.PubKey, rr chan *registry.Root, maxRetries int) {
	t.Helper()
	for attempt := 0; attempt <= maxRetries; attempt++ {
		if err := c.Subscribe(feed); err != nil {
			t.Fatalf("Subscribe failed: %v", err)
		}
		select {
		case <-rr:
			t.Logf("root received on attempt %d", attempt+1)
			return
		case <-time.After(TM):
			if attempt < maxRetries {
				t.Logf("root not received after subscribe (attempt %d/%d), retrying...", attempt+1, maxRetries+1)
				c.Unsubscribe(feed)
				time.Sleep(100 * time.Millisecond)
			}
		}
	}
	t.Fatal("root never received after all subscribe attempts")
}

func printObjects(t *testing.T, prefix string, c *skyobject.Container) {
	err := c.DB().CXDS().Iterate(
		func(key cipher.SHA256, rc uint32, _ []byte) (_ error) {
			t.Logf("[%s] %s %d", prefix, key.Hex()[:7], rc)
			return
		})
	if err != nil {
		t.Fatal(err)
	}
}

func dynamicByValue(
	t *testing.T,
	up *skyobject.Unpack,
	name string,
	obj interface{},
) (
	dr registry.Dynamic,
) {

	sch, err := up.Registry().SchemaByName(name)

	if err != nil {
		t.Fatal(err)
	}

	val := encoder.Serialize(obj)
	key := cipher.SumSHA256(val)

	dr.Schema = sch.Reference()
	dr.Hash = key

	if err := up.Set(key, val); err != nil {
		t.Fatal(err)
	}

	return

}

// with registry.Refs
func Test_send_receive_refs(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("CXO root filling is unreliable on Windows CI runners")
	}

	var (
		fr, onRootFilled   = onRootFilledToChannel(100)
		rr, onRootReceived = onRootReceivedToChannel(1)
		sn                 = getTestNode("sender")
		rconf              = getTestConfig("receiver")
	)

	rconf.TCP.Listen, rconf.UDP.Listen = "", ""       // don't listen
	rconf.OnRootFilled = onRootFilled                 // callback
	rconf.OnRootReceived = onRootReceived             // callback
	rconf.OnFillingBreaks = onFillingBreaksTestLog(t) // log

	var rn, err = NewNode(rconf)

	if err != nil {
		t.Fatal(err)
	}

	defer sn.Close() //nolint:errcheck,gosec
	defer rn.Close() //nolint:errcheck,gosec

	var pk, sk = cipher.GenerateKeyPair() //nolint:errcheck,gosec

	assertNil(t, sn.Share(pk))
	assertNil(t, rn.Share(pk))

	var (
		reg = getTestRegistry()
		sc  = sn.Container()

		up *skyobject.Unpack
	)

	if up, err = sc.Unpack(sk, reg); err != nil {
		t.Fatal(err)
	}

	var r = new(registry.Root)

	r.Nonce = 9021 // random
	r.Pub = pk     // set
	r.Descriptor = []byte("hey-ho!")

	var feed Feed

	for i := 0; i < 32; i++ {

		err := feed.Posts.AppendValues(up, Post{
			Head: fmt.Sprintf("Head #%d", i),
			Body: fmt.Sprintf("Body #%d", i),
			Time: time.Now().UnixNano(),
		})

		if err != nil {
			t.Fatal(err)
		}

	}

	r.Refs = append(r.Refs,
		dynamicByValue(t, up, "test.User", User{"Alice", 19, nil}),
		dynamicByValue(t, up, "test.Feed", feed),
	)

	// save the Root
	if err := sc.Save(up, r); err != nil {
		t.Fatal(err)
	}

	if sn.TCP().Address() == "" {
		t.Fatal("blank listening address")
	}

	// connect the nodes (synchronous from client side; wait for server to register)
	var c *Conn
	if c, err = rn.TCP().Connect(sn.TCP().Address()); err != nil {
		t.Fatal(err)
	}

	waitForCondition(t, "sender registered connection", func() bool {
		return len(sn.Connections()) > 0
	})

	// subscribe and verify root reception with retry
	subscribeWithRetry(t, c, pk, rr, 3)

	// wait for the root to be filled on the receiver (refs variant has more objects)
	select {
	case <-fr:
	case <-time.After(60 * time.Second):
		t.Log("Root :    ", r.Hash.Hex()[:7])
		t.Log("Registry: ", r.Reg.Short())

		t.Log("[sender] objects")
		printObjects(t, "sender", sc)

		t.Log("[receiver] objects")
		printObjects(t, "receiver", rn.Container())

		t.Fatal("timed out waiting for root replication")
	}
}
