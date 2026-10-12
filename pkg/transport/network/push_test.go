//go:build !(js && wasm)

package network

import (
	"bytes"
	"math/rand"
	"net"
	"testing"
	"time"

	kcp "github.com/0magnet/kcp-go/v5"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/noise"
)

// A noise encrypted KCP session reads the same through PushReaderOf as
// through Read: KCP's notify, TryRead and the noise decoder together.
func TestPushReaderOverKCP(t *testing.T) {
	l, err := kcp.ListenWithOptions("127.0.0.1:0", nil, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close() //nolint:errcheck
	accepted := make(chan *kcp.UDPSession, 1)
	go func() {
		s, err := l.AcceptKCP()
		if err == nil {
			accepted <- s
		}
	}()
	cli, err := kcp.DialWithOptions(l.Addr().String(), nil, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer cli.Close()                               //nolint:errcheck
	if _, err := cli.Write([]byte{0}); err != nil { // makes the listener accept
		t.Fatal(err)
	}
	srv := <-accepted
	defer srv.Close() //nolint:errcheck
	if _, err := srv.Read(make([]byte, 1)); err != nil {
		t.Fatal(err)
	}

	pkA, skA := cipher.GenerateKeyPair()
	pkB, skB := cipher.GenerateKeyPair()
	type res struct {
		c   net.Conn
		err error
	}
	ch := make(chan res, 1)
	go func() {
		c, err := EncryptConn(noise.Config{LocalPK: pkB, LocalSK: skB, RemotePK: pkA}, srv)
		ch <- res{c, err}
	}()
	encA, err := EncryptConn(noise.Config{LocalPK: pkA, LocalSK: skA, RemotePK: pkB, Initiator: true}, cli)
	if err != nil {
		t.Fatal(err)
	}
	r := <-ch
	if r.err != nil {
		t.Fatal(r.err)
	}
	sender := &transport{Conn: encA, rawConn: cli}
	receiver := &transport{Conn: r.c, rawConn: srv}

	rng := rand.New(rand.NewSource(3)) //nolint:gosec
	var want []byte
	msgs := make([][]byte, 2000)
	for i := range msgs {
		msgs[i] = make([]byte, 1+rng.Intn(3000))
		_, _ = rng.Read(msgs[i])
		want = append(want, msgs[i]...)
	}

	pr, ok := PushReaderOf(receiver)
	if !ok {
		t.Fatal("KCP transport cannot push")
	}
	ready := make(chan struct{}, 1)
	pr.SetNotify(func() {
		select {
		case ready <- struct{}{}:
		default:
		}
	})
	go func() {
		for _, m := range msgs {
			if _, err := sender.Write(m); err != nil {
				return
			}
		}
	}()

	var got []byte
	buf := make([]byte, 64<<10)
	emit := func(p []byte) { got = append(got, p...) }
	deadline := time.After(20 * time.Second)
	for len(got) < len(want) {
		n, err := pr.ReadPlain(buf, emit)
		if err != nil {
			t.Fatal(err)
		}
		if n > 0 {
			continue
		}
		select {
		case <-ready:
		case <-deadline:
			t.Fatalf("got %d of %d bytes", len(got), len(want))
		}
	}
	if !bytes.Equal(got, want) {
		t.Fatal("pushed stream differs from what was sent")
	}
}
