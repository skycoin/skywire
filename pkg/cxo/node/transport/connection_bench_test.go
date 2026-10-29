package transport

import (
	"io"
	"net"
	"testing"
	"time"
)

// BenchmarkNewConnection measures what an idle connection holds.
func BenchmarkNewConnection(b *testing.B) {
	a, _ := net.Pipe()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = newConnection(a, false)
	}
}

// BenchmarkConnectionSend measures queueing and writing small frames.
func BenchmarkConnectionSend(b *testing.B) {
	a, p := net.Pipe()
	go func() { _, _ = io.Copy(io.Discard, p) }() //nolint
	c := newConnection(a, false)
	defer c.Close()
	f := Frame{Head: make([]byte, 8), Body: make([]byte, 512)}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if err := c.SendFrame(f, nil, time.Minute); err != nil {
			b.Fatal(err)
		}
	}
}
