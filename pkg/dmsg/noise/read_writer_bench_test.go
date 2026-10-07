package noise

import (
	"io"
	"net"
	"testing"
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
)

// BenchmarkReadWriterFrame sends 1 KiB writes through an encrypted pipe.
func BenchmarkReadWriterFrame(b *testing.B) {
	pkI, skI := cipher.GenerateKeyPair()
	pkR, skR := cipher.GenerateKeyPair()
	nI, err := KKAndSecp256k1(Config{LocalPK: pkI, LocalSK: skI, RemotePK: pkR, Initiator: true})
	if err != nil {
		b.Fatal(err)
	}
	nR, err := KKAndSecp256k1(Config{LocalPK: pkR, LocalSK: skR, RemotePK: pkI})
	if err != nil {
		b.Fatal(err)
	}
	connI, connR := net.Pipe()
	defer connI.Close() //nolint:errcheck
	defer connR.Close() //nolint:errcheck
	rwI, rwR := NewReadWriter(connI, nI), NewReadWriter(connR, nR)
	errCh := make(chan error, 1)
	go func() { errCh <- rwR.Handshake(time.Second) }()
	if err := rwI.Handshake(time.Second); err != nil {
		b.Fatal(err)
	}
	if err := <-errCh; err != nil {
		b.Fatal(err)
	}

	msg := make([]byte, 1024)
	go func() {
		for i := 0; i < b.N; i++ {
			if _, err := rwI.Write(msg); err != nil {
				return
			}
		}
	}()
	buf := make([]byte, len(msg))
	b.ReportAllocs()
	b.SetBytes(int64(len(msg)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := io.ReadFull(rwR, buf); err != nil {
			b.Fatal(err)
		}
	}
}
