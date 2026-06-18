package got

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestParseProxyAddr(t *testing.T) {
	cases := []struct {
		name           string
		in             string
		wantAddr       string
		wantResolve    bool
		wantErrContain string
	}{
		{"bare host:port treated as socks5h (back-compat)", "127.0.0.1:4445", "127.0.0.1:4445", false, ""},
		{"socks5h scheme — proxy resolves", "socks5h://127.0.0.1:4445", "127.0.0.1:4445", false, ""},
		{"socks5 scheme — client resolves", "socks5://127.0.0.1:4445", "127.0.0.1:4445", true, ""},
		{"scheme case-insensitive", "SOCKS5H://127.0.0.1:4445", "127.0.0.1:4445", false, ""},
		{"hostname proxy ok", "socks5h://proxy.example.com:1080", "proxy.example.com:1080", false, ""},
		{"missing host", "socks5h://", "", false, "no host"},
		{"http scheme rejected", "http://127.0.0.1:8080", "", false, "unsupported proxy scheme"},
		{"unparseable", "socks5h://[::bad", "", false, "parse proxy address"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			addr, resolve, err := parseProxyAddr(c.in)
			if c.wantErrContain != "" {
				if err == nil {
					t.Fatalf("want error containing %q, got nil (addr=%q resolve=%v)", c.wantErrContain, addr, resolve)
				}
				if !contains(err.Error(), c.wantErrContain) {
					t.Fatalf("want error containing %q, got %q", c.wantErrContain, err.Error())
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if addr != c.wantAddr {
				t.Errorf("addr: want %q, got %q", c.wantAddr, addr)
			}
			if resolve != c.wantResolve {
				t.Errorf("resolveLocally: want %v, got %v", c.wantResolve, resolve)
			}
		})
	}
}

// TestDoPropagatesClient guards against the regression where Got.Do, given
// a hand-built *Download whose Client field is nil, lets Download.Init
// install a fresh DefaultClient — silently dropping any proxy / context
// configured on the Got receiver. That bypass made `got dl -x socks5h://`
// useless even after #3029 landed scheme support: the SOCKS5 dialer was
// on g.Client but g.Do never copied it into dl, so the download dialed
// directly. Verify both g.Client and g.ctx are propagated.
func TestDoPropagatesClient(t *testing.T) {
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits++
		w.Header().Set("Accept-Ranges", "none")
		_, _ = w.Write([]byte("hello")) //nolint:errcheck
	}))
	defer srv.Close()

	// A client that fails every request so we can prove dl.Client is the
	// one being used (not a quietly-installed DefaultClient).
	sentinel := errors.New("sentinel: g.Client was reached")
	stubClient := &http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
		return nil, sentinel
	})}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	g := &Got{Client: stubClient, ctx: ctx}

	// Hand-built download with no Client / ctx — Do must inject g.Client.
	dl := &Download{URL: srv.URL, Dest: t.TempDir() + "/out"}
	err := g.Do(dl)
	if err == nil {
		t.Fatalf("want sentinel error from stub client, got nil (real default client used? hits=%d)", hits)
	}
	if !errors.Is(err, sentinel) && !contains(err.Error(), sentinel.Error()) {
		t.Fatalf("want sentinel error, got %v", err)
	}
	if dl.Client != stubClient {
		t.Errorf("dl.Client was not propagated from g.Client")
	}
	if dl.ctx != ctx {
		t.Errorf("dl.ctx was not propagated from g.ctx")
	}
	if hits != 0 {
		t.Errorf("test server should not have been reached, got hits=%d", hits)
	}
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
