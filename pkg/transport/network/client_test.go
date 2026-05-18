// Package network client_test.go: unit tests for the Phase 3 Happy
// Eyeballs dialer helpers — canonicalAddr port-append behavior and
// happyEyeballsDial's v6-first / v4-fallback branches.
package network

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/skycoin/skywire/pkg/logging"
)

func TestCanonicalAddr(t *testing.T) {
	const port = "7070"
	cases := []struct {
		name string
		raw  string
		want string
	}{
		{"empty stays empty", "", ""},
		{"v4 bare host gets port", "192.0.2.1", "192.0.2.1:7070"},
		{"v4 with port unchanged", "192.0.2.1:9999", "192.0.2.1:9999"},
		{"v6 bare host gets bracketed port", "2001:db8::1", "[2001:db8::1]:7070"},
		{"v6 with bracketed port unchanged", "[2001:db8::1]:9999", "[2001:db8::1]:9999"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := canonicalAddr(tc.raw, port); got != tc.want {
				t.Fatalf("canonicalAddr(%q, %q) = %q, want %q", tc.raw, port, got, tc.want)
			}
		})
	}
}

// fakeConn satisfies net.Conn just enough for happyEyeballsDial to
// return a non-nil success value. No bytes are read or written.
type fakeConn struct{ net.Conn }

func TestHappyEyeballsDial(t *testing.T) {
	const (
		v6 = "[2001:db8::1]:7070"
		v4 = "192.0.2.1:7070"
	)
	log := logging.MustGetLogger("client_test")

	t.Run("v6 succeeds, v4 not tried", func(t *testing.T) {
		var dialed []string
		dial := func(_ context.Context, addr string) (net.Conn, error) {
			dialed = append(dialed, addr)
			return fakeConn{}, nil
		}
		conn, err := happyEyeballsDial(context.Background(), v6, v4, dial, log)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if conn == nil {
			t.Fatal("expected non-nil conn")
		}
		if len(dialed) != 1 || dialed[0] != v6 {
			t.Fatalf("expected single v6 dial, got %v", dialed)
		}
	})

	t.Run("v6 fails, v4 succeeds", func(t *testing.T) {
		var dialed []string
		dial := func(_ context.Context, addr string) (net.Conn, error) {
			dialed = append(dialed, addr)
			if addr == v6 {
				return nil, errors.New("v6 unreachable")
			}
			return fakeConn{}, nil
		}
		conn, err := happyEyeballsDial(context.Background(), v6, v4, dial, log)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if conn == nil {
			t.Fatal("expected non-nil conn")
		}
		if len(dialed) != 2 || dialed[0] != v6 || dialed[1] != v4 {
			t.Fatalf("expected v6 then v4, got %v", dialed)
		}
	})

	t.Run("both fail, error names both", func(t *testing.T) {
		var dialed []string
		dial := func(_ context.Context, addr string) (net.Conn, error) {
			dialed = append(dialed, addr)
			return nil, errors.New("nope")
		}
		_, err := happyEyeballsDial(context.Background(), v6, v4, dial, log)
		if err == nil {
			t.Fatal("expected error")
		}
		if len(dialed) != 2 {
			t.Fatalf("expected both attempts, got %v", dialed)
		}
		// Error should reference both endpoints so an operator can
		// see which v6 and v4 addrs were tried.
		msg := err.Error()
		if !containsAll(msg, v6, v4) {
			t.Fatalf("error %q missing v6 or v4 addr", msg)
		}
	})

	t.Run("v6 attempt sub-ctx does not bleed into v4", func(t *testing.T) {
		var dialed []string
		var v4Ctx context.Context
		dial := func(ctx context.Context, addr string) (net.Conn, error) {
			dialed = append(dialed, addr)
			if addr == v6 {
				// Simulate v6 attempt consuming its sub-ctx without
				// the parent ctx being canceled.
				return nil, errors.New("v6 failed fast")
			}
			v4Ctx = ctx
			return fakeConn{}, nil
		}
		parent, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if _, err := happyEyeballsDial(parent, v6, v4, dial, log); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if v4Ctx == nil {
			t.Fatal("v4 dial was not invoked")
		}
		// v4 should have inherited the parent ctx — its deadline must
		// be near the parent's 10s, not v6HeadStart's 1s.
		dl, ok := v4Ctx.Deadline()
		if !ok {
			t.Fatal("v4 ctx missing deadline")
		}
		if remaining := time.Until(dl); remaining < 5*time.Second {
			t.Fatalf("v4 ctx deadline appears narrowed to v6 sub-ctx: %v remaining", remaining)
		}
	})

	t.Run("parent ctx canceled during v6 stops short", func(t *testing.T) {
		parent, cancel := context.WithCancel(context.Background())
		cancel() // already canceled
		dial := func(ctx context.Context, _ string) (net.Conn, error) {
			return nil, ctx.Err()
		}
		_, err := happyEyeballsDial(parent, v6, v4, dial, log)
		if err == nil {
			t.Fatal("expected error from canceled parent ctx")
		}
	})
}

func containsAll(haystack string, needles ...string) bool {
	for _, n := range needles {
		found := false
		for i := 0; i+len(n) <= len(haystack); i++ {
			if haystack[i:i+len(n)] == n {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}
