package noise

import (
	"io"
	"net"
	"runtime"
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

// BenchmarkReadWriterIdleHeap reports, as retained-B/stream, what a
// ReadWriter keeps on the heap once it has written and read one message.
func BenchmarkReadWriterIdleHeap(b *testing.B) {
	pkI, skI := cipher.GenerateKeyPair()
	pkR, skR := cipher.GenerateKeyPair()
	const streams = 200
	var perStream float64
	for b.Loop() {
		var before, after runtime.MemStats
		runtime.GC()
		runtime.ReadMemStats(&before)
		keep := make([]*ReadWriter, 0, 2*streams)
		for i := 0; i < streams; i++ {
			nI, err := KKAndSecp256k1(Config{LocalPK: pkI, LocalSK: skI, RemotePK: pkR, Initiator: true})
			if err != nil {
				b.Fatal(err)
			}
			nR, err := KKAndSecp256k1(Config{LocalPK: pkR, LocalSK: skR, RemotePK: pkI})
			if err != nil {
				b.Fatal(err)
			}
			connI, connR := net.Pipe()
			rwI, rwR := NewReadWriter(connI, nI), NewReadWriter(connR, nR)
			errCh := make(chan error, 1)
			go func() { errCh <- rwR.Handshake(time.Second) }()
			if err := rwI.Handshake(time.Second); err != nil {
				b.Fatal(err)
			}
			if err := <-errCh; err != nil {
				b.Fatal(err)
			}
			go rwI.Write(make([]byte, 100)) //nolint:errcheck
			if _, err := io.ReadFull(rwR, make([]byte, 100)); err != nil {
				b.Fatal(err)
			}
			keep = append(keep, rwI, rwR)
		}
		runtime.GC()
		runtime.ReadMemStats(&after)
		perStream = float64(after.HeapAlloc-before.HeapAlloc) / float64(len(keep))
		runtime.KeepAlive(keep)
	}
	b.ReportMetric(perStream, "retained-B/stream")
}

// BenchmarkReadWriterTCPBulk streams 64 KiB writes over loopback TCP, where
// one socket read can return many frames.
func BenchmarkReadWriterTCPBulk(b *testing.B) {
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
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		b.Fatal(err)
	}
	defer l.Close() //nolint:errcheck
	accepted := make(chan net.Conn, 1)
	go func() {
		c, _ := l.Accept() //nolint:errcheck
		accepted <- c
	}()
	connI, err := net.Dial("tcp", l.Addr().String())
	if err != nil {
		b.Fatal(err)
	}
	connR := <-accepted
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

	msg := make([]byte, 64<<10)
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
