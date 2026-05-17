// Package dmsgpty pkg/dmsg/dmsgpty/dialer_test.go — pins the StreamDialer
// abstraction added so the outbound proxy-dial side of Host is
// pluggable. Phase 1 contract: the dialer field is what gets invoked
// on ExecRemote / handleProxy paths, not the bound *dmsg.Client.
package dmsgpty

import (
	"context"
	"errors"
	"net"
	"testing"

	"github.com/skycoin/skywire/pkg/cipher"
)

// fakeDialer captures the (pk, port) of each DialStream call and
// returns a fixed err. The err short-circuits the rest of ExecRemote
// before it touches the conn — the test only needs to prove the
// dialer field is what gets reached, not that an actual PtyClient
// boots over the returned conn.
type fakeDialer struct {
	calls   []fakeDialCall
	dialErr error
}

type fakeDialCall struct {
	pk   cipher.PubKey
	port uint16
}

func (f *fakeDialer) DialStream(_ context.Context, pk cipher.PubKey, port uint16) (net.Conn, error) {
	f.calls = append(f.calls, fakeDialCall{pk: pk, port: port})
	return nil, f.dialErr
}

func TestHost_ExecRemote_RoutesThroughInjectedDialer(t *testing.T) {
	// Custom dialer fed via NewHostWithDialer is what ExecRemote
	// invokes — proves the Host doesn't reach past the abstraction
	// to its bound dmsg.Client for the outbound side.
	pk, _ := cipher.GenerateKeyPair()
	want := errors.New("fake dial failed")
	fake := &fakeDialer{dialErr: want}

	// dmsgC stays nil because the listening side isn't exercised;
	// only the outbound path is under test. If anything reached for
	// h.dmsgC.DialStream pre-refactor, this test would NPE — the
	// existence of a non-NPE failure surface is the contract.
	h := NewHostWithDialer(nil, nil, fake)

	_, err := h.ExecRemote(context.Background(), pk, 22, &CommandExecReq{Name: "true"})
	if err == nil {
		t.Fatal("ExecRemote: want error from injected dialer, got nil")
	}
	if !errors.Is(err, want) {
		t.Errorf("ExecRemote err = %v, want it to wrap %v", err, want)
	}
	if len(fake.calls) != 1 {
		t.Fatalf("dialer calls: got %d, want 1", len(fake.calls))
	}
	got := fake.calls[0]
	if got.pk != pk {
		t.Errorf("dialer call pk = %s, want %s", got.pk, pk)
	}
	if got.port != 22 {
		t.Errorf("dialer call port = %d, want 22", got.port)
	}
}

func TestHost_ExecRemote_DefaultPortFallback(t *testing.T) {
	// rPort=0 falls back to DefaultPort (matches pre-refactor
	// ExecRemote behavior). The fallback happens BEFORE the dialer
	// is invoked, so the dialer sees the resolved port.
	pk, _ := cipher.GenerateKeyPair()
	fake := &fakeDialer{dialErr: errors.New("stop early")}
	h := NewHostWithDialer(nil, nil, fake)

	_, _ = h.ExecRemote(context.Background(), pk, 0, &CommandExecReq{Name: "true"}) //nolint:errcheck
	if len(fake.calls) != 1 {
		t.Fatalf("dialer calls: got %d, want 1", len(fake.calls))
	}
	if got := fake.calls[0].port; got != DefaultPort {
		t.Errorf("port with rPort=0: got %d, want DefaultPort=%d", got, DefaultPort)
	}
}
