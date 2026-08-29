// Package skysocks pkg/skysocks/rangesplit_counters_test.go
package skysocks

import (
	"bytes"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// newRSTestClientH mirrors newRSTestClient but also returns the *Client so a test
// can read its range-split observability counters after a request.
func newRSTestClientH(t *testing.T, backendAddr string, conc int, chunk int64) (string, *Client) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close() //nolint:errcheck
	dialed := make(chan net.Conn, 1)
	go func() {
		c, _ := ln.Accept() //nolint:errcheck
		dialed <- c
	}()
	cliConn, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatalf("dial pair: %v", err)
	}
	exitConn := <-dialed
	go rsFakeExit(t, exitConn, backendAddr)

	client, err := NewClient(cliConn, nil)
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	client.SetRangeSplit(true, conc, chunk)
	t.Cleanup(func() { _ = client.Close() }) //nolint:errcheck

	probe, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("probe listen: %v", err)
	}
	proxyAddr := probe.Addr().String()
	probe.Close()                                        //nolint:errcheck,gosec
	go func() { _ = client.ListenAndServe(proxyAddr) }() //nolint:errcheck
	for i := 0; i < 100; i++ {
		if cc, err := net.Dial("tcp", proxyAddr); err == nil {
			cc.Close() //nolint:errcheck,gosec
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	return proxyAddr, client
}

// TestRangeSplitCountersFire: a committed multi-chunk split increments the
// observability counters the status page surfaces (proxystatus.RangeSplit), so
// "is range-split firing" is a readable field, not just a Debugf.
func TestRangeSplitCountersFire(t *testing.T) {
	const blobSize = 20 << 20 // 20 MiB
	blob := make([]byte, blobSize)
	for i := range blob {
		blob[i] = byte(i*31 + 7)
	}
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Etag", "\"blobv1\"")
		http.ServeContent(w, r, "blob.bin", time.Unix(0, 0), bytes.NewReader(blob))
	}))
	defer backend.Close()

	const conc, chunk = 4, 1 << 20 // 20 chunks over 4 streams
	proxy, client := newRSTestClientH(t, backend.Listener.Addr().String(), conc, chunk)

	// Before any request: enabled, nothing split yet.
	if rs := client.rangeSplitSnapshot(); rs == nil || !rs.Enabled || rs.TotalSplits != 0 {
		t.Fatalf("pre-request snapshot = %+v, want enabled with zero splits", rs)
	}

	resp := socks5Get(t, proxy, "example.com", "/blob.bin")
	defer resp.Body.Close() //nolint:errcheck
	if _, err := io.Copy(io.Discard, resp.Body); err != nil {
		t.Fatalf("drain body: %v", err)
	}

	// ActiveSplits is decremented just after the final byte is delivered, so poll
	// briefly for it to settle rather than racing the fetch goroutine's exit.
	var rs = client.rangeSplitSnapshot()
	for i := 0; i < 100 && rs != nil && rs.ActiveSplits != 0; i++ {
		time.Sleep(10 * time.Millisecond)
		rs = client.rangeSplitSnapshot()
	}
	if rs == nil {
		t.Fatal("post-request snapshot is nil")
	}
	if rs.TotalSplits != 1 {
		t.Errorf("TotalSplits = %d, want 1", rs.TotalSplits)
	}
	if rs.TotalChunks != 20 {
		t.Errorf("TotalChunks = %d, want 20", rs.TotalChunks)
	}
	if rs.TotalBytes != blobSize {
		t.Errorf("TotalBytes = %d, want %d", rs.TotalBytes, blobSize)
	}
	if rs.StreamsPerSplit != conc {
		t.Errorf("StreamsPerSplit = %d, want %d", rs.StreamsPerSplit, conc)
	}
	if rs.ChunkSize != chunk {
		t.Errorf("ChunkSize = %d, want %d", rs.ChunkSize, chunk)
	}
	if rs.ActiveSplits != 0 {
		t.Errorf("ActiveSplits = %d, want 0 after completion", rs.ActiveSplits)
	}
}

// TestRangeSplitSnapshotDisabled: with the feature off the snapshot is nil, so
// the status page omits the section entirely.
func TestRangeSplitSnapshotDisabled(t *testing.T) {
	c := &Client{}
	c.SetRangeSplit(false, 0, 0)
	if rs := c.rangeSplitSnapshot(); rs != nil {
		t.Fatalf("disabled snapshot = %+v, want nil", rs)
	}
}
