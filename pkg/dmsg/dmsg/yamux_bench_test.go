package dmsg

import (
	"io"
	"net"
	"testing"

	"github.com/0magnet/yamux"
)

// BenchmarkYamuxStream writes 1 KiB frames over one stream of a session
// configured as dmsg configures it.
func BenchmarkYamuxStream(b *testing.B) {
	connA, connB := net.Pipe()
	client, err := yamux.Client(connA, YamuxConfig())
	if err != nil {
		b.Fatal(err)
	}
	defer client.Close() //nolint:errcheck
	server, err := yamux.Server(connB, YamuxConfig())
	if err != nil {
		b.Fatal(err)
	}
	defer server.Close() //nolint:errcheck

	accepted := make(chan net.Conn, 1)
	go func() {
		s, err := server.Accept()
		if err != nil {
			close(accepted)
			return
		}
		accepted <- s
	}()
	w, err := client.OpenStream()
	if err != nil {
		b.Fatal(err)
	}
	defer w.Close() //nolint:errcheck
	if _, err := w.Write([]byte{0}); err != nil {
		b.Fatal(err)
	}
	r, ok := <-accepted
	if !ok {
		b.Fatal("accept failed")
	}
	defer r.Close() //nolint:errcheck
	if _, err := io.ReadFull(r, make([]byte, 1)); err != nil {
		b.Fatal(err)
	}

	msg := make([]byte, 1024)
	go func() {
		for i := 0; i < b.N; i++ {
			if _, err := w.Write(msg); err != nil {
				return
			}
		}
	}()
	buf := make([]byte, len(msg))
	b.ReportAllocs()
	b.SetBytes(int64(len(msg)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := io.ReadFull(r, buf); err != nil {
			b.Fatal(err)
		}
	}
}
