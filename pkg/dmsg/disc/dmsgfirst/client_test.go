package dmsgfirst

import (
	"context"
	"errors"
	"net"
	"net/url"
	"slices"
	"testing"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/disc"
	"github.com/skycoin/skywire/pkg/logging"
)

// stubClient is a programmable disc.APIClient: every call records the
// method invoked and returns the configured (entry|err). Tests assert
// on which leg (primary vs fallback) was called for each error class.
type stubClient struct {
	name        string
	entryRet    *disc.Entry
	entryErr    error
	calls       []string
	postEntryFn func(*disc.Entry) error
}

func (s *stubClient) record(method string) { s.calls = append(s.calls, s.name+":"+method) }
func (s *stubClient) Entry(_ context.Context, _ cipher.PubKey) (*disc.Entry, error) {
	s.record("Entry")
	return s.entryRet, s.entryErr
}
func (s *stubClient) PostEntry(_ context.Context, e *disc.Entry) error {
	s.record("PostEntry")
	if s.postEntryFn != nil {
		return s.postEntryFn(e)
	}
	return s.entryErr
}
func (s *stubClient) PutEntry(_ context.Context, _ cipher.SecKey, _ *disc.Entry) error {
	s.record("PutEntry")
	return s.entryErr
}
func (s *stubClient) DelEntry(_ context.Context, _ *disc.Entry) error {
	s.record("DelEntry")
	return s.entryErr
}
func (s *stubClient) AvailableServers(_ context.Context) ([]*disc.Entry, error) {
	s.record("AvailableServers")
	return nil, s.entryErr
}
func (s *stubClient) AllServers(_ context.Context) ([]*disc.Entry, error) {
	s.record("AllServers")
	return nil, s.entryErr
}
func (s *stubClient) AllEntries(_ context.Context) ([]string, error) {
	s.record("AllEntries")
	return nil, s.entryErr
}
func (s *stubClient) AllClientsByServer(_ context.Context) (map[string][]*disc.Entry, error) {
	s.record("AllClientsByServer")
	return nil, s.entryErr
}
func (s *stubClient) ClientsByServer(_ context.Context, _ cipher.PubKey) ([]*disc.Entry, error) {
	s.record("ClientsByServer")
	return nil, s.entryErr
}

// timeoutNetErr satisfies net.Error and reports Timeout()=true so the
// shouldFallback path that types-asserts on net.Error is exercised.
type timeoutNetErr struct{}

func (timeoutNetErr) Error() string   { return "i/o timeout" }
func (timeoutNetErr) Timeout() bool   { return true }
func (timeoutNetErr) Temporary() bool { return true }

func newFallback(t *testing.T, primary, fallback disc.APIClient) *fallbackClient {
	t.Helper()
	return &fallbackClient{
		primary:  primary,
		fallback: fallback,
		log:      logging.MustGetLogger("test"),
	}
}

// TestShouldFallback covers the error-class taxonomy that decides
// when to retry on the fallback leg vs surface primary's error.
func TestShouldFallback(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},

		// Authoritative — never fallback.
		{"ErrKeyNotFound", disc.ErrKeyNotFound, false},
		{"ErrNoAvailableServers", disc.ErrNoAvailableServers, false},
		{"ErrUnauthorized", disc.ErrUnauthorized, false},
		{"ErrBadInput", disc.ErrBadInput, false},
		{"EntryValidationError", disc.NewEntryValidationError("bad sig"), false},

		// Transport-class — fallback.
		{"net.Error timeout", timeoutNetErr{}, true},
		{"url.Error connection refused", &url.Error{Op: "Get", URL: "http://x", Err: errors.New("connection refused")}, true},
		{"raw connection refused string", errors.New("dial tcp 1.2.3.4:80: connect: connection refused"), true},
		{"no route to host", errors.New("connect: no route to host"), true},
		{"i/o timeout string", errors.New("read tcp: i/o timeout"), true},
		{"connection reset", errors.New("read tcp: connection reset by peer"), true},
		{"broken pipe", errors.New("write tcp: broken pipe"), true},
		{"no such host", errors.New("dial tcp: lookup x: no such host"), true},
		{"context deadline exceeded", context.DeadlineExceeded, true},

		// Random unrelated error — DON'T fallback (we can't tell if
		// it's authoritative or transport, so prefer to surface it).
		{"random", errors.New("totally unrelated bug"), false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := shouldFallback(c.err); got != c.want {
				t.Errorf("shouldFallback(%v) = %v, want %v", c.err, got, c.want)
			}
		})
	}
}

// TestEntryRoutesPrimaryThenFallback proves that a transport-class
// error on primary triggers a fallback call, while an authoritative
// error doesn't.
func TestEntryRoutesPrimaryThenFallback(t *testing.T) {
	pk, _ := cipher.GenerateKeyPair()

	// Case 1: primary succeeds — fallback is never called.
	t.Run("primary ok", func(t *testing.T) {
		good := &disc.Entry{Static: pk}
		p := &stubClient{name: "p", entryRet: good}
		f := &stubClient{name: "f"}
		c := newFallback(t, p, f)
		got, err := c.Entry(context.Background(), pk)
		if err != nil || got != good {
			t.Fatalf("Entry: got=%v err=%v", got, err)
		}
		if !equalCalls(p.calls, []string{"p:Entry"}) {
			t.Errorf("primary calls=%v", p.calls)
		}
		if len(f.calls) != 0 {
			t.Errorf("fallback should not have been called: %v", f.calls)
		}
	})

	// Case 2: primary returns ErrKeyNotFound (authoritative) —
	// fallback NOT called, error bubbles through.
	t.Run("authoritative not-found", func(t *testing.T) {
		p := &stubClient{name: "p", entryErr: disc.ErrKeyNotFound}
		f := &stubClient{name: "f"}
		c := newFallback(t, p, f)
		_, err := c.Entry(context.Background(), pk)
		if !errors.Is(err, disc.ErrKeyNotFound) {
			t.Fatalf("want ErrKeyNotFound, got %v", err)
		}
		if len(f.calls) != 0 {
			t.Errorf("fallback should not have been called: %v", f.calls)
		}
	})

	// Case 3: primary returns connection-refused — fallback IS
	// called, returns a hit.
	t.Run("primary unreachable, fallback ok", func(t *testing.T) {
		good := &disc.Entry{Static: pk}
		p := &stubClient{name: "p", entryErr: errors.New("dial: connection refused")}
		f := &stubClient{name: "f", entryRet: good}
		c := newFallback(t, p, f)
		got, err := c.Entry(context.Background(), pk)
		if err != nil || got != good {
			t.Fatalf("Entry: got=%v err=%v", got, err)
		}
		if !equalCalls(p.calls, []string{"p:Entry"}) || !equalCalls(f.calls, []string{"f:Entry"}) {
			t.Errorf("calls primary=%v fallback=%v (want both)", p.calls, f.calls)
		}
	})

	// Case 4: both fail with transport errors — return fallback's err.
	t.Run("both unreachable", func(t *testing.T) {
		p := &stubClient{name: "p", entryErr: timeoutNetErr{}}
		f := &stubClient{name: "f", entryErr: errors.New("dial: connection refused")}
		c := newFallback(t, p, f)
		_, err := c.Entry(context.Background(), pk)
		if err == nil {
			t.Fatal("want error, got nil")
		}
	})
}

// TestAllMethodsForwardCorrectly ensures every APIClient method on
// fallbackClient delegates to the same-named method on primary, then
// fallback on transport error. Catches the dumb copy-paste bug where
// e.g. PostEntry calls primary.PutEntry.
func TestAllMethodsForwardCorrectly(t *testing.T) {
	pk, _ := cipher.GenerateKeyPair()
	sk, _ := cipher.GenerateKeyPair()
	_ = sk
	transport := errors.New("dial: connection refused")

	type call struct {
		name string
		fn   func(c *fallbackClient) error
	}
	calls := []call{
		{"Entry", func(c *fallbackClient) error { _, e := c.Entry(context.Background(), pk); return e }},
		{"PostEntry", func(c *fallbackClient) error { return c.PostEntry(context.Background(), &disc.Entry{}) }},
		{"PutEntry", func(c *fallbackClient) error { return c.PutEntry(context.Background(), cipher.SecKey{}, &disc.Entry{}) }},
		{"DelEntry", func(c *fallbackClient) error { return c.DelEntry(context.Background(), &disc.Entry{}) }},
		{"AvailableServers", func(c *fallbackClient) error { _, e := c.AvailableServers(context.Background()); return e }},
		{"AllServers", func(c *fallbackClient) error { _, e := c.AllServers(context.Background()); return e }},
		{"AllEntries", func(c *fallbackClient) error { _, e := c.AllEntries(context.Background()); return e }},
		{"AllClientsByServer", func(c *fallbackClient) error { _, e := c.AllClientsByServer(context.Background()); return e }},
		{"ClientsByServer", func(c *fallbackClient) error { _, e := c.ClientsByServer(context.Background(), pk); return e }},
	}
	for _, k := range calls {
		t.Run(k.name, func(t *testing.T) {
			p := &stubClient{name: "p", entryErr: transport}
			f := &stubClient{name: "f"}
			c := newFallback(t, p, f)
			_ = k.fn(c) //nolint:errcheck // primary returns the transport error; this test asserts on which leg was called, not the propagated error
			wantPrim := []string{"p:" + k.name}
			wantFall := []string{"f:" + k.name}
			if !equalCalls(p.calls, wantPrim) {
				t.Errorf("primary calls=%v want=%v", p.calls, wantPrim)
			}
			if !equalCalls(f.calls, wantFall) {
				t.Errorf("fallback calls=%v want=%v", f.calls, wantFall)
			}
		})
	}
}

// TestNewWithoutDmsgClientDegradesToHTTP confirms that calling New
// with a nil *dmsg.Client (e.g. a tool that hasn't bootstrapped
// dmsg) returns the plain HTTP client unchanged. That matches the
// documented degrade-to-HTTP-only contract.
func TestNewWithoutDmsgClientDegradesToHTTP(t *testing.T) {
	// We can't easily exercise the dmsg path here (would require a
	// running dmsg-server fixture). But we can verify the nil-dmsgC
	// branch by constructing with nil and checking the returned
	// type is the underlying disc.NewHTTP value, not a fallbackClient.
	pk, _ := cipher.GenerateKeyPair()
	c := New(nil, pk, "http://example.invalid", nil, nil)
	if _, ok := c.(*fallbackClient); ok {
		t.Errorf("expected New(nil,...) to return the plain HTTP client, got *fallbackClient")
	}
}

// equalCalls compares two []string for full sequence equality.
func equalCalls(a, b []string) bool {
	return slices.Equal(a, b)
}

// Sanity check: ensure the package compiles its test file with the
// imports we've declared.
var _ net.Error = timeoutNetErr{}
