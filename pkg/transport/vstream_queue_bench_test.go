package transport

import "testing"

var benchStream *VStream

// BenchmarkVStreamOpen measures what one idle stream holds.
func BenchmarkVStreamOpen(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		benchStream = &VStream{rq: newFrameQueue(), closed: make(chan struct{})}
	}
}

// BenchmarkVStreamDeliverRead moves frames from deliver to Read.
func BenchmarkVStreamDeliverRead(b *testing.B) {
	m := &VStreamMux{}
	s := &VStream{rq: newFrameQueue(), closed: make(chan struct{}), mux: m}
	frame := make([]byte, 1400)
	go func() {
		for i := 0; i < b.N; i++ {
			m.deliver(s, frame)
		}
	}()
	buf := make([]byte, 1400)
	b.ReportAllocs()
	b.SetBytes(int64(len(frame)))
	for i := 0; i < b.N; i++ {
		if _, err := s.Read(buf); err != nil {
			b.Fatal(err)
		}
	}
}
