// Package disc pkg/dmsg/disc/recursion_test.go c1-net-dmsg
package disc

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/logging"
)

// loopTransport is a dmsg transport that needs a discovery entry before it can
// dial — so it asks the same client that is waiting on it. This is the shape
// of the real cycle, with the dmsg machinery replaced by the one thing about
// it that matters: the dial performs a lookup.
type loopTransport struct {
	client *httpClient
	pk     cipher.PubKey
	depth  int
	t      *testing.T
}

func (l *loopTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	l.depth++
	if l.depth > 50 {
		// Without the guard this never stops; in a test it would exhaust the
		// stack and take the whole run with it, so bail and report.
		l.t.Fatal("discovery lookup recursed without bound — the guard did not hold")
	}
	// The dial needs the entry for the peer it is dialing.
	_, err := l.client.Entry(req.Context(), l.pk)
	if err != nil {
		return nil, err
	}
	return nil, errors.New("unreachable")
}

// TestCircularLookupIsRefused: a dmsg-addressed discovery reached from inside
// its own dial must fail, not recurse.
//
// The bug this guards produced "fatal error: stack overflow" about six seconds
// into every visor start, on a config whose discovery URL sat in the plain
// HTTP field instead of the dmsg one. A misconfigured address should cost an
// error, not the process.
func TestCircularLookupIsRefused(t *testing.T) {
	pk, _ := cipher.GenerateKeyPair()
	lt := &loopTransport{pk: pk, t: t}
	c := &httpClient{
		address: "dmsg://02857a87410b3709eddb99bc00508cc7cfeb588d5ed87e3ef9d2aac838ad49470a:80",
		client:  &http.Client{Transport: lt},
		log:     logging.MustGetLogger("disc_recursion_test"),
	}
	lt.client = c

	_, err := c.Entry(context.Background(), pk)
	if err == nil {
		t.Fatal("a circular discovery lookup was allowed to succeed")
	}
	if !errors.Is(err, ErrCircularDiscovery) {
		t.Fatalf("got %v, want ErrCircularDiscovery", err)
	}
	if lt.depth != 1 {
		t.Fatalf("the transport was entered %d times; the guard should stop it after the first", lt.depth)
	}
}

// TestPlainHTTPNestedLookupIsAllowed: the escape from the cycle must stay
// open. A nested lookup against a plain-HTTP discovery dials TCP, needs no
// entry of its own, and is exactly how a dmsg dial gets the answer it is
// stuck on — refusing it would break the fallback instead of the loop.
func TestPlainHTTPNestedLookupIsAllowed(t *testing.T) {
	pk, _ := cipher.GenerateKeyPair()
	reached := false
	c := &httpClient{
		address: "http://discovery.example:9090",
		client: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			reached = true
			return nil, errors.New("dialed, which is all this test needs")
		})},
		log: logging.MustGetLogger("disc_recursion_test"),
	}

	// Pretend a discovery request is already running up the chain.
	ctx := withRequestInFlight(context.Background())
	_, err := c.Entry(ctx, pk)
	if errors.Is(err, ErrCircularDiscovery) {
		t.Fatal("a plain-HTTP nested lookup was refused; that is the way out of the cycle")
	}
	if !reached {
		t.Fatal("the plain-HTTP request never reached its transport")
	}
}

// TestOverDmsg pins which addresses count as riding a dmsg transport.
func TestOverDmsg(t *testing.T) {
	for addr, want := range map[string]bool{
		"dmsg://02857a87:80":    true,
		"DMSG://02857a87:80":    true,
		"http://127.0.0.1:9090": false,
		"https://disc.example":  false,
		"":                      false,
		"tcp://not-dmsg":        false,
		"notdmsg://02857a87:80": false,
	} {
		if got := overDmsg(addr); got != want {
			t.Errorf("overDmsg(%q) = %v, want %v", addr, got, want)
		}
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
