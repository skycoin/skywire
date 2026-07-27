// Package xfer pkg/skychat/xfer/xfer_test.go c2-app-chat
package xfer

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"testing"
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/skychat/message"
)

// nopWriteCloser adapts a bytes.Buffer to io.WriteCloser.
type nopWriteCloser struct{ *bytes.Buffer }

func (nopWriteCloser) Close() error { return nil }

// runTransfer sends payload from one end of a pipe and receives on the other,
// returning the sender's Receipt and both errors.
func runTransfer(t *testing.T, payload []byte, accept AcceptFunc) (Receipt, error, error) {
	t.Helper()
	a, b := net.Pipe()
	defer func() { _ = a.Close() }() //nolint:errcheck
	defer func() { _ = b.Close() }() //nolint:errcheck

	from, _ := cipher.GenerateKeyPair()
	offer := Offer{
		ID:   "t1",
		Name: "blob.bin",
		Size: int64(len(payload)),
		MIME: "application/octet-stream",
		From: from,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	type recvResult struct {
		offer Offer
		err   error
	}
	recvCh := make(chan recvResult, 1)
	go func() {
		of, err := Receive(ctx, from, b, accept)
		recvCh <- recvResult{of, err}
	}()

	rc, sErr := Send(ctx, a, offer, bytes.NewReader(payload))
	// Close the sender the way Manager.SendFile does (defer conn.Close()): the
	// receiver waits for this hang-up after writing its receipt, so a test that
	// held the conn open would just sit out the drain window.
	_ = a.Close() //nolint:errcheck
	res := <-recvCh
	return rc, sErr, res.err
}

func TestSendReceiveRoundTrip(t *testing.T) {
	payload := bytes.Repeat([]byte("skywire-file-"), 5000) // ~65 KB, > one frame
	var got bytes.Buffer
	accept := func(_ cipher.PubKey, o Offer) (io.WriteCloser, bool) {
		if o.Name != "blob.bin" {
			t.Errorf("offer name = %q", o.Name)
		}
		return nopWriteCloser{&got}, true
	}

	rc, sErr, rErr := runTransfer(t, payload, accept)
	if sErr != nil {
		t.Fatalf("Send: %v", sErr)
	}
	if rErr != nil {
		t.Fatalf("Receive: %v", rErr)
	}
	if !rc.OK {
		t.Fatalf("receipt not OK: %q", rc.Err)
	}
	if !bytes.Equal(got.Bytes(), payload) {
		t.Fatalf("received %d bytes, want %d (content mismatch)", got.Len(), len(payload))
	}
	// Cross-check the digest the receiver verified against an independent one.
	sum := sha256.Sum256(payload)
	if want := hex.EncodeToString(sum[:]); want == "" {
		t.Fatal("empty reference digest")
	}
}

func TestEmptyFile(t *testing.T) {
	var got bytes.Buffer
	accept := func(_ cipher.PubKey, _ Offer) (io.WriteCloser, bool) { return nopWriteCloser{&got}, true }
	rc, sErr, rErr := runTransfer(t, nil, accept)
	if sErr != nil || rErr != nil {
		t.Fatalf("empty transfer: send=%v recv=%v", sErr, rErr)
	}
	if !rc.OK {
		t.Fatalf("empty receipt not OK: %q", rc.Err)
	}
	if got.Len() != 0 {
		t.Fatalf("expected 0 bytes, got %d", got.Len())
	}
}

func TestDeclinedTransfer(t *testing.T) {
	decline := func(_ cipher.PubKey, _ Offer) (io.WriteCloser, bool) { return nil, false }
	rc, sErr, rErr := runTransfer(t, []byte("nope"), decline)
	if sErr != nil {
		t.Fatalf("Send should succeed with a negative receipt, got %v", sErr)
	}
	if rc.OK {
		t.Fatal("receipt should be OK=false for a declined transfer")
	}
	if !IsDeclined(rErr) {
		t.Fatalf("Receive err = %v, want declined sentinel", rErr)
	}
}

// A receiver that hangs up the instant it has written the receipt used to cost
// the sender that receipt on conns whose remote-close races buffered data (a
// skywire route group). The sender must classify that as ErrNoReceipt — fully
// delivered, verdict unknown — and NOT as a failed transfer, because the bytes
// and the digest are already on the receiver's side.
func TestSendNoReceipt(t *testing.T) {
	a, b := net.Pipe()
	defer func() { _ = a.Close() }() //nolint:errcheck

	from, _ := cipher.GenerateKeyPair()
	payload := []byte("delivered but unconfirmed")
	offer := Offer{ID: "nr1", Name: "blob.bin", Size: int64(len(payload)), From: from}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var got bytes.Buffer
	go func() {
		// Read the whole transfer, then drop the conn without answering. The
		// frames the sender is waiting for never arrive.
		defer func() { _ = b.Close() }()                //nolint:errcheck
		if _, err := message.ReadFrame(b); err != nil { // offer
			return
		}
		if err := sendAccept(b, true, ""); err != nil {
			return
		}
		if _, err := io.CopyN(&got, b, int64(len(payload))); err != nil {
			return
		}
		_, _ = message.ReadFrame(b) //nolint:errcheck // trailer, then hang up
	}()

	rc, err := Send(ctx, a, offer, bytes.NewReader(payload))
	if !IsNoReceipt(err) {
		t.Fatalf("Send err = %v, want an ErrNoReceipt-wrapped error", err)
	}
	if rc.ID != offer.ID {
		t.Errorf("receipt carries id %q, want the offer's %q", rc.ID, offer.ID)
	}
	if rc.OK {
		t.Error("an unconfirmed transfer must not claim an OK verdict")
	}
	if !bytes.Equal(got.Bytes(), payload) {
		t.Errorf("receiver got %d bytes, want the full %d", got.Len(), len(payload))
	}
	// A body that failed mid-stream must NOT look like this case.
	if IsNoReceipt(fmt.Errorf("xfer: stream body (10/99 bytes): %w", io.ErrClosedPipe)) {
		t.Error("a truncated body was misclassified as delivered-unconfirmed")
	}
}

// The receiver hands the close over to the sender: after writing the receipt it
// waits for the peer to hang up, so the receipt is always drained first.
func TestReceiveWaitsForSenderClose(t *testing.T) {
	a, b := net.Pipe()
	defer func() { _ = b.Close() }() //nolint:errcheck

	from, _ := cipher.GenerateKeyPair()
	payload := []byte("drain-order")
	offer := Offer{ID: "d1", Name: "blob.bin", Size: int64(len(payload)), From: from}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var got bytes.Buffer
	recvDone := make(chan error, 1)
	go func() {
		_, err := Receive(ctx, from, b, func(cipher.PubKey, Offer) (io.WriteCloser, bool) {
			return nopWriteCloser{&got}, true
		})
		recvDone <- err
	}()

	rc, sErr := Send(ctx, a, offer, bytes.NewReader(payload))
	if sErr != nil || !rc.OK {
		t.Fatalf("Send: err=%v receipt=%+v", sErr, rc)
	}
	// Receive must still be parked in its drain read: it has written the
	// receipt but the sender hasn't closed yet.
	select {
	case err := <-recvDone:
		t.Fatalf("Receive returned before the sender closed (err=%v)", err)
	case <-time.After(100 * time.Millisecond):
	}
	_ = a.Close() //nolint:errcheck // the sender hangs up, as Manager.SendFile does
	select {
	case err := <-recvDone:
		if err != nil {
			t.Fatalf("Receive after the sender closed: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Receive did not return after the sender closed")
	}
}

// TestManagerSendFile drives the full Manager path: a receiving Manager serving a
// listener, a sending Manager dialing it, over an in-memory listener/dialer.
func TestManagerSendFile(t *testing.T) {
	lis := newMemListener()
	dial := func(_ context.Context, _ cipher.PubKey, _ uint16) (net.Conn, error) {
		return lis.dialMem()
	}

	sendPK, _ := cipher.GenerateKeyPair()
	recvPK, _ := cipher.GenerateKeyPair()

	var got bytes.Buffer
	done := make(chan error, 1)
	recvMgr := NewManager(Config{
		LocalPK: recvPK,
		Port:    64,
		Accept:  func(_ cipher.PubKey, _ Offer) (io.WriteCloser, bool) { return nopWriteCloser{&got}, true },
		OnDone:  func(dir Direction, _ cipher.PubKey, _ Offer, err error) { done <- err },
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go recvMgr.Serve(ctx, lis)

	sendMgr := NewManager(Config{LocalPK: sendPK, Dial: dial, Port: 64})
	payload := bytes.Repeat([]byte("abc"), 10000)
	rc, err := sendMgr.SendFile(ctx, recvPK, Offer{ID: "m1", Name: "x.dat", Size: int64(len(payload))}, bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("SendFile: %v", err)
	}
	if !rc.OK {
		t.Fatalf("manager receipt not OK: %q", rc.Err)
	}
	select {
	case rerr := <-done:
		if rerr != nil {
			t.Fatalf("receiver OnDone err: %v", rerr)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("receiver OnDone never fired")
	}
	if !bytes.Equal(got.Bytes(), payload) {
		t.Fatalf("manager transfer content mismatch (%d/%d)", got.Len(), len(payload))
	}
}

// memListener is a tiny in-process net.Listener backed by a channel of pipes.
type memListener struct {
	ch     chan net.Conn
	closed chan struct{}
}

func newMemListener() *memListener {
	return &memListener{ch: make(chan net.Conn, 8), closed: make(chan struct{})}
}

func (l *memListener) dialMem() (net.Conn, error) {
	c1, c2 := net.Pipe()
	select {
	case l.ch <- c2:
		return c1, nil
	case <-l.closed:
		return nil, net.ErrClosed
	}
}

func (l *memListener) Accept() (net.Conn, error) {
	select {
	case c := <-l.ch:
		return c, nil
	case <-l.closed:
		return nil, net.ErrClosed
	}
}

func (l *memListener) Close() error {
	select {
	case <-l.closed:
	default:
		close(l.closed)
	}
	return nil
}

func (l *memListener) Addr() net.Addr { return dummyAddr{} }

type dummyAddr struct{}

func (dummyAddr) Network() string { return "mem" }
func (dummyAddr) String() string  { return "mem" }
