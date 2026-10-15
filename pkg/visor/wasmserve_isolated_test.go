package visor

import (
	"context"
	"net"
	"net/http"
	"testing"
	"time"
)

// TestBrowseOriginIsolatedOnlyWhenAsked checks that browse origins carry the
// isolation headers for an isolated desk and nothing new for any other desk.
func TestBrowseOriginIsolatedOnlyWhenAsked(t *testing.T) {
	for _, isolated := range []bool{false, true} {
		l, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		addr := l.Addr().String()
		_ = l.Close() //nolint:errcheck

		ctx, cancel := context.WithCancel(context.Background())
		go func() {
			_ = ServeBrowseOrigin(ctx, BrowseOriginConfig{ //nolint:errcheck
				Addr: addr, VOrigin: "https://desk.example", Suffix: ".browse.example", Isolated: isolated,
			})
		}()

		var resp *http.Response
		for i := 0; i < 50; i++ {
			req, _ := http.NewRequest(http.MethodGet, "http://"+addr+"/", nil) //nolint:errcheck
			req.Host = "abc.browse.example"
			if resp, err = http.DefaultClient.Do(req); err == nil {
				break
			}
			time.Sleep(20 * time.Millisecond)
		}
		cancel()
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close() //nolint:errcheck

		got := resp.Header.Get("Cross-Origin-Embedder-Policy")
		if isolated && got != "credentialless" {
			t.Fatalf("isolated: COEP = %q, want credentialless", got)
		}
		if !isolated && got != "" {
			t.Fatalf("not isolated: COEP = %q, want none", got)
		}
		if isolated && resp.Header.Get("Cross-Origin-Resource-Policy") != "cross-origin" {
			t.Fatal("isolated: CORP cross-origin missing")
		}
	}
}
