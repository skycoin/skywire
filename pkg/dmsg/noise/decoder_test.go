package noise

import (
	"bytes"
	"errors"
	"math/rand"
	"net"
	"testing"
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
)

func handshakenPair(t *testing.T) (rwI, rwR *ReadWriter, connR net.Conn) {
	t.Helper()
	pkI, skI := cipher.GenerateKeyPair()
	pkR, skR := cipher.GenerateKeyPair()
	nI, err := KKAndSecp256k1(Config{LocalPK: pkI, LocalSK: skI, RemotePK: pkR, Initiator: true})
	if err != nil {
		t.Fatal(err)
	}
	nR, err := KKAndSecp256k1(Config{LocalPK: pkR, LocalSK: skR, RemotePK: pkI})
	if err != nil {
		t.Fatal(err)
	}
	connI, connR := net.Pipe()
	t.Cleanup(func() { _ = connI.Close(); _ = connR.Close() }) //nolint:errcheck
	rwI, rwR = NewReadWriter(connI, nI), NewReadWriter(connR, nR)
	errCh := make(chan error, 1)
	go func() { errCh <- rwR.Handshake(time.Second) }()
	if err := rwI.Handshake(time.Second); err != nil {
		t.Fatal(err)
	}
	if err := <-errCh; err != nil {
		t.Fatal(err)
	}
	return rwI, rwR, connR
}

// The stream decodes the same whether read or pushed in arbitrary pieces,
// including plaintext and ciphertext that Read had already buffered.
func TestDecoderMatchesRead(t *testing.T) {
	rwI, rwR, connR := handshakenPair(t)
	rng := rand.New(rand.NewSource(1)) //nolint:gosec

	var want []byte
	msgs := make([][]byte, 200)
	for i := range msgs {
		msgs[i] = make([]byte, 1+rng.Intn(9000))
		rng.Read(msgs[i]) //nolint:errcheck
		want = append(want, msgs[i]...)
	}
	go func() {
		for _, m := range msgs {
			if _, err := rwI.Write(m); err != nil {
				return
			}
		}
	}()

	// Read part of the first message the usual way, leaving buffered state.
	got := make([]byte, 100)
	if _, err := readAllOf(rwR, got); err != nil {
		t.Fatal(err)
	}
	dec, pending := rwR.Decoder()
	got = append(got, pending...)
	if _, err := rwR.Read(make([]byte, 1)); !errors.Is(err, ErrReadDetached) {
		t.Fatalf("Read after Decoder: %v", err)
	}

	emit := func(p []byte) { got = append(got, p...) }
	if err := dec.Feed(nil, emit); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 3000)
	for len(got) < len(want) {
		n, err := connR.Read(buf[:1+rng.Intn(len(buf))])
		if err != nil {
			t.Fatal(err)
		}
		if err := dec.Feed(buf[:n], emit); err != nil {
			t.Fatal(err)
		}
	}
	if !bytes.Equal(got, want) {
		t.Fatal("decoded stream differs")
	}
}

func readAllOf(r *ReadWriter, b []byte) (int, error) {
	n := 0
	for n < len(b) {
		m, err := r.Read(b[n:])
		n += m
		if err != nil {
			return n, err
		}
	}
	return n, nil
}

// A corrupted frame is a final error.
func TestDecoderRejectsBadFrame(t *testing.T) {
	_, rwR, _ := handshakenPair(t)
	dec, _ := rwR.Decoder()
	frame := []byte{0, 20}
	frame = append(frame, make([]byte, 20)...)
	if err := dec.Feed(frame, func([]byte) {}); err == nil {
		t.Fatal("bad frame accepted")
	}
	if err := dec.Feed(frame, func([]byte) {}); err == nil {
		t.Fatal("error not final")
	}
}
