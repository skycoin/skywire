//go:build !tinygo || (js && wasm)

package router

import (
	"context"
	"net"
	"net/rpc"
	"testing"
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
	gobrpc "github.com/skycoin/skywire/pkg/gobrpc"
	"github.com/skycoin/skywire/pkg/transport"
	tptypes "github.com/skycoin/skywire/pkg/transport/types"
)

// servePipe registers rcvr under transportQueryRPCName on one end of an
// in-memory pipe (the same net/rpc server + gob codec the dmsg listener uses)
// and returns a gobrpc client bound to the other end — exercising the full
// serialize → RPC → deserialize path without needing a real dmsg transport.
func servePipe(t *testing.T, rcvr interface{}) *gobrpc.Client {
	t.Helper()
	srvConn, cliConn := net.Pipe()
	rpcS := rpc.NewServer()
	if err := rpcS.RegisterName(transportQueryRPCName, rcvr); err != nil {
		t.Fatalf("register: %v", err)
	}
	go rpcS.ServeConn(srvConn)
	cli := gobrpc.NewClient(cliConn)
	t.Cleanup(func() { _ = cli.Close() })
	return cli
}

func TestTransportQueryGateway_RPCRoundTrip_Valid(t *testing.T) {
	rsnPK, rsnSK := cipher.GenerateKeyPair()
	dstPK, _ := cipher.GenerateKeyPair()
	srcPK, _ := cipher.GenerateKeyPair()

	// Real gateway on D. TM is nil (no transports to advertise) — the verify +
	// response-shape path is what this exercises over the wire.
	gw := &TransportQueryRPCGateway{
		LocalPK:     dstPK,
		TrustedRSNs: func() []cipher.PubKey { return []cipher.PubKey{rsnPK} },
		TM:          nil,
	}
	cli := servePipe(t, gw)

	q := &TransportQuery{RSNPK: rsnPK, TargetPK: dstPK, RequesterPK: srcPK, Nonce: 5}
	if err := q.Sign(rsnSK); err != nil {
		t.Fatalf("sign: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var reply TransportQueryResponse
	if err := rpcCall(ctx, cli, transportQueryRPCName+".Query", q, &reply); err != nil {
		t.Fatalf("Query RPC: %v", err)
	}
	if reply.TargetPK != dstPK {
		t.Fatalf("reply.TargetPK = %s, want %s", reply.TargetPK, dstPK)
	}
}

func TestTransportQueryGateway_RPCRoundTrip_Untrusted(t *testing.T) {
	rsnPK, rsnSK := cipher.GenerateKeyPair()
	otherRSN, _ := cipher.GenerateKeyPair()
	dstPK, _ := cipher.GenerateKeyPair()
	srcPK, _ := cipher.GenerateKeyPair()

	// D trusts only otherRSN, but the query is signed by rsnPK → RPC must error.
	gw := &TransportQueryRPCGateway{
		LocalPK:     dstPK,
		TrustedRSNs: func() []cipher.PubKey { return []cipher.PubKey{otherRSN} },
		TM:          nil,
	}
	cli := servePipe(t, gw)

	q := &TransportQuery{RSNPK: rsnPK, TargetPK: dstPK, RequesterPK: srcPK, Nonce: 6}
	if err := q.Sign(rsnSK); err != nil {
		t.Fatalf("sign: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var reply TransportQueryResponse
	err := rpcCall(ctx, cli, transportQueryRPCName+".Query", q, &reply)
	if err == nil {
		t.Fatal("expected RPC error for untrusted RSN, got nil")
	}
	if err.Error() != ErrTransportQueryUntrustedRSN.Error() {
		t.Fatalf("error = %q, want %q", err.Error(), ErrTransportQueryUntrustedRSN.Error())
	}
}

// stubQueryGW returns canned CompactEntries, proving the gob codec used by the
// dmsg deliverer round-trips a []transport.CompactEntry response faithfully.
type stubQueryGW struct{ resp TransportQueryResponse }

func (s *stubQueryGW) Query(_ *TransportQuery, reply *TransportQueryResponse) error {
	*reply = s.resp
	return nil
}

func TestTransportQueryGateway_CompactEntriesOverWire(t *testing.T) {
	dstPK, _ := cipher.GenerateKeyPair()
	peer1, _ := cipher.GenerateKeyPair()
	peer2, _ := cipher.GenerateKeyPair()

	want := TransportQueryResponse{
		TargetPK: dstPK,
		Entries: []transport.CompactEntry{
			{Remote: peer1, Type: tptypes.STCPR},
			{Remote: peer2, Type: tptypes.SUDPH, Label: transport.LabelUser},
		},
	}
	cli := servePipe(t, &stubQueryGW{resp: want})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var reply TransportQueryResponse
	if err := rpcCall(ctx, cli, transportQueryRPCName+".Query", &TransportQuery{}, &reply); err != nil {
		t.Fatalf("Query RPC: %v", err)
	}
	if len(reply.Entries) != 2 {
		t.Fatalf("entries = %d, want 2", len(reply.Entries))
	}
	if reply.Entries[0].Remote != peer1 || reply.Entries[0].Type != tptypes.STCPR {
		t.Fatalf("entry0 = %+v", reply.Entries[0])
	}
	if reply.Entries[1].Remote != peer2 || reply.Entries[1].Label != transport.LabelUser {
		t.Fatalf("entry1 = %+v", reply.Entries[1])
	}

	// The requester reconstructs full entries from the compact response.
	entries := reconstructDstEntries(dstPK, &reply)
	if len(entries) != 2 || !entries[0].HasEdge(dstPK) || !entries[0].HasEdge(peer1) {
		t.Fatalf("reconstruct failed: %+v", entries)
	}
}
