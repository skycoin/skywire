package api

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"math/big"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/quic-go/quic-go"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/deployment/ar/arfeed"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/transport/network/addrresolver"
	types "github.com/skycoin/skywire/pkg/transport/types"
)

// fakeProber answers probes from a table and records what it was asked.
type fakeProber struct {
	mu     sync.Mutex
	open   map[string]bool
	calls  []string
	called chan string
}

func (f *fakeProber) probe(_ context.Context, _ string, target string) bool {
	f.mu.Lock()
	f.calls = append(f.calls, target)
	ok := f.open[target]
	f.mu.Unlock()
	f.called <- target
	return ok
}

func newTestBook(t *testing.T, open map[string]bool) (*reachBook, *fakeProber) {
	t.Helper()
	fp := &fakeProber{open: open, called: make(chan string, 16)}
	b := newReachBook(logging.MustGetLogger("reach-test"))
	b.probe = fp.probe
	t.Cleanup(b.close)
	return b, fp
}

func waitProbe(t *testing.T, fp *fakeProber) string {
	t.Helper()
	select {
	case target := <-fp.called:
		return target
	case <-time.After(5 * time.Second):
		t.Fatal("probe did not run")
	}
	return ""
}

// record returns the peer's published record, after in-flight probe
// results have landed.
func record(b *reachBook, pk cipher.PubKey) *arfeed.Reach {
	b.mu.Lock()
	defer b.mu.Unlock()
	p := b.peers[pk]
	if p == nil {
		return nil
	}
	return p.record(time.Now())
}

func eventually(t *testing.T, cond func() bool) {
	t.Helper()
	require.Eventually(t, cond, 5*time.Second, 10*time.Millisecond)
}

func TestReachBookProbesStcprOnce(t *testing.T) {
	b, fp := newTestBook(t, map[string]bool{"203.0.113.7:7777": true})
	pk, _ := cipher.GenerateKeyPair()
	data := addrresolver.VisorData{RemoteAddr: "203.0.113.7", LocalAddresses: addrresolver.LocalAddresses{Port: "7777"}}

	b.noteBind(types.STCPR, pk, data)
	require.Equal(t, "203.0.113.7:7777", waitProbe(t, fp))
	eventually(t, func() bool {
		r := record(b, pk)
		return r != nil && len(r.Open) == 1 && r.Open[0] == arfeed.TypeSTCPR
	})

	// Keepalive refreshes of the same binding do not probe again.
	for i := 0; i < 5; i++ {
		b.noteBind(types.STCPR, pk, data)
	}
	select {
	case target := <-fp.called:
		t.Fatalf("re-probed %s on a keepalive", target)
	case <-time.After(100 * time.Millisecond):
	}
	require.Equal(t, []string{arfeed.TypeSTCPR}, record(b, pk).Bound)
}

func TestReachBookClosedOnlyAfterRepeatedMisses(t *testing.T) {
	b, fp := newTestBook(t, nil) // nothing answers
	pk, _ := cipher.GenerateKeyPair()
	data := addrresolver.VisorData{RemoteAddr: "203.0.113.8", LocalAddresses: addrresolver.LocalAddresses{Port: "30178"}}

	b.noteBind(types.QUIC, pk, data)
	waitProbe(t, fp)
	eventually(t, func() bool {
		b.mu.Lock()
		defer b.mu.Unlock()
		return !b.peers[pk].probes[arfeed.TypeQUIC].queued
	})
	r := record(b, pk)
	require.Empty(t, r.Closed, "one miss must not publish Closed")
	require.Equal(t, arfeed.Untested, r.Verdict(arfeed.TypeQUIC, "", time.Now()))

	// Force the retry due and the per-target gap past.
	b.mu.Lock()
	b.peers[pk].probes[arfeed.TypeQUIC].next = time.Time{}
	delete(b.lastTarget, "203.0.113.8:30178")
	b.mu.Unlock()
	b.noteBind(types.QUIC, pk, data)
	waitProbe(t, fp)
	eventually(t, func() bool {
		r := record(b, pk)
		return len(r.Closed) == 1 && r.Closed[0] == arfeed.TypeQUIC
	})
	require.Equal(t, arfeed.Unreachable, record(b, pk).Verdict(arfeed.TypeQUIC, "", time.Now()))
}

func TestReachBookMovedAddressVoidsVerdict(t *testing.T) {
	b, fp := newTestBook(t, map[string]bool{"203.0.113.9:7777": true})
	pk, _ := cipher.GenerateKeyPair()
	b.noteBind(types.STCPR, pk, addrresolver.VisorData{RemoteAddr: "203.0.113.9", LocalAddresses: addrresolver.LocalAddresses{Port: "7777"}})
	waitProbe(t, fp)
	eventually(t, func() bool { return len(record(b, pk).Open) == 1 })

	b.noteBind(types.STCPR, pk, addrresolver.VisorData{RemoteAddr: "203.0.113.10", LocalAddresses: addrresolver.LocalAddresses{Port: "7777"}})
	require.Equal(t, "203.0.113.10:7777", waitProbe(t, fp))
	eventually(t, func() bool {
		b.mu.Lock()
		defer b.mu.Unlock()
		return !b.peers[pk].probes[arfeed.TypeSTCPR].queued
	})
	require.Empty(t, record(b, pk).Open, "the old address's verdict must not carry over")
}

func TestReachBookLiveDeclAndSweep(t *testing.T) {
	b, _ := newTestBook(t, nil)
	pk, _ := cipher.GenerateKeyPair()

	b.noteBind(types.SUDPH, pk, addrresolver.VisorData{RemoteAddr: "198.51.100.1:40000"})
	b.setLive(pk, true)
	b.noteDecl(pk, arfeed.ReachDecl{NAT: arfeed.NATPortRestricted, Accepts: []string{"sudph", "stcpr"},
		Inbound: map[string]int64{"sudph": time.Now().Unix()}})

	r := record(b, pk)
	require.True(t, r.Live)
	require.Equal(t, []string{"stcpr", "sudph"}, r.Accepts, "accepts are sorted")
	require.Zero(t, r.Inbound["sudph"]%int64(arfeed.InboundGranularity/time.Second), "inbound is rounded")
	require.Equal(t, arfeed.Confirmed, r.Verdict(arfeed.TypeSUDPH, arfeed.NATSymmetric, time.Now()),
		"a recent inbound confirmation outranks the NAT rule")

	// An unchanged declaration does not dirty the bucket.
	b.mu.Lock()
	b.dirty = map[int]struct{}{}
	b.mu.Unlock()
	b.noteDecl(pk, arfeed.ReachDecl{NAT: arfeed.NATPortRestricted, Accepts: []string{"stcpr", "sudph"},
		Inbound: r.Inbound})
	b.mu.Lock()
	require.Empty(t, b.dirty)
	b.mu.Unlock()

	b.setLive(pk, false)
	require.False(t, record(b, pk).Live)

	// Everything ages out: the peer goes.
	b.mu.Lock()
	b.sweep(time.Now().Add(bindTTL + declTTL + time.Minute))
	_, still := b.peers[pk]
	b.mu.Unlock()
	require.False(t, still)
}

func TestReachBookBucketsRoundTrip(t *testing.T) {
	b, _ := newTestBook(t, nil)
	pk, _ := cipher.GenerateKeyPair()
	b.setLive(pk, true)
	b.noteDecl(pk, arfeed.ReachDecl{NAT: arfeed.NATFullCone})

	ops := b.buckets(map[int]struct{}{arfeed.ReachBucket(pk): {}}, time.Now())
	require.Len(t, ops, 1)
	require.Equal(t, arfeed.ReachPath(arfeed.ReachBucket(pk)), ops[0].Path)
	got, err := arfeed.DecodeReachBucket(ops[0].Value)
	require.NoError(t, err)
	require.True(t, got[pk].Live)
	require.Equal(t, arfeed.NATFullCone, got[pk].NAT)

	again := b.buckets(map[int]struct{}{arfeed.ReachBucket(pk): {}}, time.Now())
	require.Equal(t, ops[0].Value, again[0].Value, "equal content must encode to equal bytes")
}

func TestProbeTargetTCP(t *testing.T) {
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	go func() {
		for {
			c, err := lis.Accept()
			if err != nil {
				return
			}
			_ = c.Close() //nolint:errcheck
		}
	}()
	addr := lis.Addr().String()
	ctx, cancel := context.WithTimeout(context.Background(), probeTimeout)
	defer cancel()
	require.True(t, probeTarget(ctx, arfeed.TypeSTCPR, addr))

	require.NoError(t, lis.Close())
	require.False(t, probeTarget(ctx, arfeed.TypeSTCPR, addr))
}

func selfSignedTLS(t *testing.T) *tls.Config {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(1), NotAfter: time.Now().Add(time.Hour)}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	require.NoError(t, err)
	return &tls.Config{
		Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}},
		NextProtos:   []string{"probe-test"},
	}
}

// TestProbeQUICAgainstQuicGo: a quic-go listener answers the probe's unknown
// version with a version negotiation; a silent UDP port does not.
func TestProbeQUICAgainstQuicGo(t *testing.T) {
	udp, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	require.NoError(t, err)
	lis, err := quic.Listen(udp, selfSignedTLS(t), &quic.Config{})
	require.NoError(t, err)
	defer lis.Close() //nolint:errcheck

	ctx, cancel := context.WithTimeout(context.Background(), probeTimeout)
	defer cancel()
	require.True(t, probeQUIC(ctx, udp.LocalAddr().String()))

	silent, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	require.NoError(t, err)
	defer silent.Close() //nolint:errcheck
	ctx2, cancel2 := context.WithTimeout(context.Background(), time.Second)
	defer cancel2()
	require.False(t, probeQUIC(ctx2, silent.LocalAddr().String()))
}
