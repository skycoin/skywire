// Package appnet pkg/app/appnet/dial_fallback_test.go
package appnet

import (
	"context"
	"errors"
	"net"
	"testing"

	"github.com/skycoin/skywire/pkg/cipher"
)

func TestDialWithFallback(t *testing.T) {
	pk, _ := cipher.GenerateKeyPair()

	t.Run("first network wins, no fallback attempted", func(t *testing.T) {
		var tried []Type
		dial := func(_ context.Context, a Addr) (net.Conn, error) {
			tried = append(tried, a.Net)
			return &net.TCPConn{}, nil
		}
		conn, used, err := DialWithFallback(context.Background(), dial, pk, 1, TypeSkynet, TypeDmsg)
		if err != nil || conn == nil {
			t.Fatalf("expected success, got conn=%v err=%v", conn, err)
		}
		if used != TypeSkynet {
			t.Errorf("expected skynet to carry, got %q", used)
		}
		if len(tried) != 1 || tried[0] != TypeSkynet {
			t.Errorf("fallback must not be attempted when the first net succeeds; tried=%v", tried)
		}
	})

	t.Run("falls through to dmsg when skynet fails", func(t *testing.T) {
		var tried []Type
		dial := func(_ context.Context, a Addr) (net.Conn, error) {
			tried = append(tried, a.Net)
			if a.Net == TypeSkynet {
				return nil, errors.New("skynet down")
			}
			return &net.TCPConn{}, nil
		}
		conn, used, err := DialWithFallback(context.Background(), dial, pk, 1, TypeSkynet, TypeDmsg)
		if err != nil || conn == nil {
			t.Fatalf("expected dmsg fallback success, got err=%v", err)
		}
		if used != TypeDmsg {
			t.Errorf("expected dmsg to carry, got %q", used)
		}
		if len(tried) != 2 {
			t.Errorf("expected both nets tried in order, got %v", tried)
		}
	})

	t.Run("no fallback net → only skynet tried", func(t *testing.T) {
		var tried []Type
		dial := func(_ context.Context, a Addr) (net.Conn, error) {
			tried = append(tried, a.Net)
			return nil, errors.New("down")
		}
		_, _, err := DialWithFallback(context.Background(), dial, pk, 1, TypeSkynet)
		if err == nil {
			t.Fatal("expected error when the only net fails")
		}
		if len(tried) != 1 {
			t.Errorf("expected only skynet tried, got %v", tried)
		}
	})

	t.Run("canceled context stops before dialing", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		called := false
		dial := func(_ context.Context, _ Addr) (net.Conn, error) {
			called = true
			return &net.TCPConn{}, nil
		}
		if _, _, err := DialWithFallback(ctx, dial, pk, 1, TypeSkynet, TypeDmsg); err == nil {
			t.Error("expected context error")
		}
		if called {
			t.Error("dial must not be attempted once ctx is canceled")
		}
	})
}

func TestOtherNet(t *testing.T) {
	if OtherNet(TypeSkynet) != TypeDmsg {
		t.Error("skynet's other net is dmsg")
	}
	if OtherNet(TypeDmsg) != TypeSkynet {
		t.Error("dmsg's other net is skynet")
	}
	if OtherNet("bogus") != "" {
		t.Error("unknown net has no alternate")
	}
}
