package commands

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// A handler that waits on another visor must outlive the server's WriteTimeout,
// or the client sees the connection drop with no status.
func TestWaitsOnPeerOutlivesWriteTimeout(t *testing.T) {
	slow := func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(300 * time.Millisecond)
		_, _ = w.Write([]byte("ok")) //nolint:errcheck
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/plain", slow)
	mux.HandleFunc("/peer", waitsOnPeer(slow))
	srv := httptest.NewUnstartedServer(mux)
	srv.Config.WriteTimeout = 100 * time.Millisecond
	srv.Start()
	defer srv.Close()

	if resp, err := http.Get(srv.URL + "/plain"); err == nil { //nolint:noctx
		body, _ := io.ReadAll(resp.Body) //nolint:errcheck
		_ = resp.Body.Close()            //nolint:errcheck
		if string(body) == "ok" {
			t.Fatal("plain handler answered past WriteTimeout; the test no longer exercises the deadline")
		}
	}

	resp, err := http.Get(srv.URL + "/peer") //nolint:noctx
	if err != nil {
		t.Fatalf("peer-bound handler was cut off: %v", err)
	}
	defer resp.Body.Close() //nolint:errcheck
	body, err := io.ReadAll(resp.Body)
	if err != nil || string(body) != "ok" {
		t.Fatalf("got %q, %v; want ok", body, err)
	}
}
