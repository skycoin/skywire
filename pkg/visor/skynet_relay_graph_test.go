package visor

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/transport"
)

// edgeCounter answers GetTransportsByEdge slowly and counts the queries; the
// rest of the interface is never called here.
type edgeCounter struct {
	transport.DiscoveryClient
	calls   atomic.Int32
	entries []*transport.Entry
	err     error
}

func (f *edgeCounter) GetTransportsByEdge(context.Context, cipher.PubKey) ([]*transport.Entry, error) {
	f.calls.Add(1)
	time.Sleep(50 * time.Millisecond)
	return f.entries, f.err
}

func TestRelayGraph_OneQueryForConcurrentLookups(t *testing.T) {
	lpk, _ := cipher.GenerateKeyPair()
	remote, _ := cipher.GenerateKeyPair()
	relay, _ := cipher.GenerateKeyPair()
	dc := &edgeCounter{entries: []*transport.Entry{
		{Edges: transport.SortEdges(remote, relay), Type: "stcpr"},
		{Edges: transport.SortEdges(remote, lpk), Type: "dmsg"},
	}}
	c := &relayGraphCache{m: make(map[cipher.PubKey]*relayGraphEntry)}

	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			peers, err := c.peers(context.Background(), dc, lpk, remote)
			if err != nil {
				t.Error(err)
				return
			}
			if _, ok := peers[relay]; !ok || len(peers) != 1 {
				t.Errorf("peers = %v, want only the stcpr relay", peers)
			}
		}()
	}
	wg.Wait()
	if n := dc.calls.Load(); n != 1 {
		t.Fatalf("%d TPD queries for 12 concurrent lookups, want 1", n)
	}

	if _, err := c.peers(context.Background(), dc, lpk, remote); err != nil {
		t.Fatal(err)
	}
	if n := dc.calls.Load(); n != 1 {
		t.Fatalf("a lookup within the TTL queried again (%d queries)", n)
	}
}

func TestRelayGraph_FailureIsNotKept(t *testing.T) {
	lpk, _ := cipher.GenerateKeyPair()
	remote, _ := cipher.GenerateKeyPair()
	dc := &edgeCounter{err: errors.New("tpd down")}
	c := &relayGraphCache{m: make(map[cipher.PubKey]*relayGraphEntry)}

	for i := 0; i < 2; i++ {
		if _, err := c.peers(context.Background(), dc, lpk, remote); err == nil {
			t.Fatal("want the query's error")
		}
	}
	if n := dc.calls.Load(); n != 2 {
		t.Fatalf("%d queries after two failed lookups, want 2", n)
	}
}
