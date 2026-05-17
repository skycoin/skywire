// Package visor pkg/visor/init_dmsg_skywire_test.go — pins the
// Phase 3 wiring contract: PK extractor refuses non-skywire conns
// (defense-in-depth before the whitelist gate), and the dmsgpty
// dialer chain is skywire-first / dmsg-fallback.
package visor

import (
	"net"
	"testing"

	"github.com/skycoin/skywire/pkg/app/appnet"
	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/dmsgpty"
	"github.com/skycoin/skywire/pkg/routing"
)

// fakeAppnetConn is a net.Conn whose RemoteAddr returns an
// appnet.Addr — what SkywireConn would return in production. Only
// RemoteAddr is exercised; the rest of the surface is unused by the
// extractor.
type fakeAppnetConn struct {
	net.Conn
	addr appnet.Addr
}

func (f *fakeAppnetConn) RemoteAddr() net.Addr { return f.addr }

// fakeOtherConn returns a non-appnet.Addr from RemoteAddr — what
// e.g. a raw TCP conn would do. Used to confirm the extractor
// REJECTS such conns rather than passing a zero PK through to the
// whitelist gate.
type fakeOtherConn struct {
	net.Conn
}

func (fakeOtherConn) RemoteAddr() net.Addr {
	return &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 22}
}

func TestSkywireConnPK_ExtractsFromAppnetAddr(t *testing.T) {
	pk, _ := cipher.GenerateKeyPair()
	conn := &fakeAppnetConn{addr: appnet.Addr{
		Net:    appnet.TypeSkynet,
		PubKey: pk,
		Port:   routing.Port(22),
	}}
	got, ok := skywireConnPK(conn)
	if !ok {
		t.Fatal("extractor returned ok=false on a valid appnet.Addr conn")
	}
	if got != pk {
		t.Errorf("extractor pk = %s, want %s", got, pk)
	}
}

func TestSkywireConnPK_RejectsNonAppnetConn(t *testing.T) {
	// Defense-in-depth: a foreign conn type (e.g. a raw TCP
	// hand-off) must NOT pass extraction, because the whitelist
	// gate downstream would otherwise check a zero PK that
	// trivially fails closed — but masking this at extraction is
	// the more honest signal: foreign conns shouldn't reach the
	// whitelist at all.
	got, ok := skywireConnPK(fakeOtherConn{})
	if ok {
		t.Errorf("extractor accepted non-appnet.Addr conn: got pk=%s", got)
	}
}

func TestBuildDmsgptyDialer_SkywireFirstDmsgFallback(t *testing.T) {
	// Chain order is the load-bearing contract: skywire MUST be
	// tried before dmsg so operators with a route to the target
	// peer get the transport-aware path; dmsg is only the
	// fallback. Swapping the order would silently regress to
	// dmsg-only behavior for every call where skywire would
	// succeed.
	chain, ok := buildDmsgptyDialer(nil).(dmsgpty.MultiDialer)
	if !ok {
		t.Fatalf("buildDmsgptyDialer: got %T, want dmsgpty.MultiDialer", chain)
	}
	if len(chain) != 2 {
		t.Fatalf("chain length = %d, want 2 (skywire + dmsg)", len(chain))
	}
	if _, isSkywire := chain[0].(skywireDialer); !isSkywire {
		t.Errorf("chain[0] = %T, want skywireDialer (transport-aware must come first)", chain[0])
	}
	// chain[1] is the dmsg adapter (unexported type in dmsgpty);
	// asserting on type name isn't useful — just confirm it's
	// non-nil. The presence of 2 entries plus skywire-first is
	// the meaningful contract.
	if chain[1] == nil {
		t.Error("chain[1] is nil; dmsg fallback missing")
	}
}

// Compile-time confirmation the extractor matches dmsgpty.PKExtractor.
// Catches signature drift if either side changes shape.
var _ dmsgpty.PKExtractor = skywireConnPK
