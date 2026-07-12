//go:build !tinygo

package network

import (
	"context"
	"errors"
	"io"
	"net"
	"testing"
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
	types "github.com/skycoin/skywire/pkg/transport/types"
)

// TestWSCarrier_RoundTrip exercises the native WS carrier: the WS-server-as-
// net.Listener bridge (wsListener) accepts a coder/websocket dial (wsDial) and
// the resulting net.Conn carries bytes both ways. This is the carrier the WS
// transport type wraps in Noise+yamux via initTransport.
func TestWSCarrier_RoundTrip(t *testing.T) {
	lis, err := newWSListener("127.0.0.1:0")
	if err != nil {
		t.Fatalf("newWSListener: %v", err)
	}
	defer lis.Close() //nolint:errcheck

	accepted := make(chan net.Conn, 1)
	go func() {
		c, aerr := lis.Accept()
		if aerr != nil {
			accepted <- nil
			return
		}
		accepted <- c
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cli, err := wsDial(ctx, "ws://"+lis.Addr().String()+"/")
	if err != nil {
		t.Fatalf("wsDial: %v", err)
	}
	defer cli.Close() //nolint:errcheck

	srv := <-accepted
	if srv == nil {
		t.Fatal("listener Accept returned no conn")
	}
	defer srv.Close() //nolint:errcheck

	// client -> server
	if _, err := cli.Write([]byte("ping")); err != nil {
		t.Fatalf("client write: %v", err)
	}
	buf := make([]byte, 4)
	if err := readFull(srv, buf); err != nil {
		t.Fatalf("server read: %v", err)
	}
	if string(buf) != "ping" {
		t.Fatalf("server got %q, want ping", buf)
	}

	// server -> client
	if _, err := srv.Write([]byte("pong")); err != nil {
		t.Fatalf("server write: %v", err)
	}
	buf2 := make([]byte, 4)
	if err := readFull(cli, buf2); err != nil {
		t.Fatalf("client read: %v", err)
	}
	if string(buf2) != "pong" {
		t.Fatalf("client got %q, want pong", buf2)
	}
}

func readFull(c net.Conn, b []byte) error {
	// Generous deadline (matches the sibling WS/dmsg round-trip tests) so a
	// slow/loaded CI runner doesn't flake mid-handshake.
	if err := c.SetReadDeadline(time.Now().Add(10 * time.Second)); err != nil {
		return err
	}
	_, err := io.ReadFull(c, b)
	return err
}

// TestWSDialNilTableNoPanic guards the fleet-wide crash where a native visor —
// which starts the WS client to ACCEPT but never populates a WSTable — panicked
// on any `tp add -t ws` because wsClient.Dial called a method on the nil table
// interface. Dial must return ErrWSEntryNotFound, not panic.
func TestWSDialNilTableNoPanic(t *testing.T) {
	c := newWS(&genericClient{netType: types.WS}, nil)
	_, err := c.Dial(context.Background(), cipher.PubKey{}, 0)
	if !errors.Is(err, ErrWSEntryNotFound) {
		t.Fatalf("nil-table WS Dial: want ErrWSEntryNotFound, got %v", err)
	}
}
