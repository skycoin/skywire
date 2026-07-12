package skynet

import (
	"net"
	"sync"
	"testing"
)

// TestServeCloseOverlapRace closes the server with no settling delay so Close's
// activeConn.Wait races Serve's Accept + activeConn.Add — the interleaving a
// Windows CI runner hit. Guards against a regression of the Add-after-Wait race
// (run under -race). The plain settle-then-close path is TestServerServeAndClose.
func TestServeCloseOverlapRace(t *testing.T) {
	const iters = 300
	for i := 0; i < iters; i++ {
		s := NewServer(testLog())
		lis, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		serveErr := make(chan error, 1)
		go func() { serveErr <- s.Serve(lis) }()

		var wg sync.WaitGroup
		wg.Add(1)
		go func() {
			defer wg.Done()
			if c, derr := net.Dial("tcp", lis.Addr().String()); derr == nil {
				_ = c.Close() //nolint:errcheck
			}
		}()

		// No settling delay: Close overlaps Accept + activeConn.Add.
		if cerr := s.Close(); cerr != nil {
			t.Fatalf("Close: %v", cerr)
		}
		wg.Wait()
		<-serveErr
	}
}
