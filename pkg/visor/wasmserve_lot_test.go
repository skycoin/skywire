package visor

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

// TestLotProxyKeepsTheDeskInCharge proxies a site that sends its own
// isolation headers, a cookie and a root-relative redirect, as
// next.linuxontab.com does.
func TestLotProxyKeepsTheDeskInCharge(t *testing.T) {
	site := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cross-Origin-Embedder-Policy", "require-corp")
		w.Header().Set("Cross-Origin-Resource-Policy", "same-origin")
		w.Header().Set("Set-Cookie", "a=b")
		if r.Header.Get("Cookie") != "" {
			t.Error("the desk's cookies reached the site")
		}
		switch r.URL.Path {
		case "/wasm.html":
			http.Redirect(w, r, "/wasm?"+r.URL.RawQuery, http.StatusPermanentRedirect) //nolint:gosec // the test's own fake site
		case "/wasm":
			_, _ = io.WriteString(w, "page "+r.URL.RawQuery) //nolint:errcheck,gosec // the test's own fake site
		default:
			http.NotFound(w, r)
		}
	}))
	defer site.Close()

	lot, err := lotHandler(site.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.Handle("/lot/", lot)
	desk := httptest.NewServer(crossOriginIsolated(mux))
	defer desk.Close()

	req, _ := http.NewRequest(http.MethodGet, desk.URL+"/lot/wasm.html?wisp=vnet:6001", nil) //nolint:errcheck
	req.Header.Set("Cookie", "desk=secret")
	resp, err := (&http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close() //nolint:errcheck
	if got := resp.Header.Get("Location"); got != "/lot/wasm?wisp=vnet:6001" {
		t.Fatalf("Location = %q, want /lot/wasm?wisp=vnet:6001", got)
	}

	resp, err = http.Get(desk.URL + "/lot/wasm?wisp=vnet:6001") //nolint:gosec,noctx
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body) //nolint:errcheck
	_ = resp.Body.Close()            //nolint:errcheck
	if string(body) != "page wisp=vnet:6001" {
		t.Fatalf("body = %q", body)
	}
	for h, want := range map[string]string{"Cross-Origin-Embedder-Policy": "credentialless", "Cross-Origin-Resource-Policy": "cross-origin"} {
		if got := resp.Header.Values(h); len(got) != 1 || got[0] != want {
			t.Fatalf("%s = %q, want only %q", h, got, want)
		}
	}
	if resp.Header.Get("Set-Cookie") != "" {
		t.Fatal("the site set a cookie on the desk")
	}
}

func TestLotLocation(t *testing.T) {
	up, err := url.Parse("https://next.linuxontab.com")
	if err != nil {
		t.Fatal(err)
	}
	for in, want := range map[string]string{
		"/wasm?a=1":                       "/lot/wasm?a=1",
		"https://next.linuxontab.com/x#f": "/lot/x#f",
		"https://elsewhere.example/x":     "https://elsewhere.example/x",
		"relative/path":                   "relative/path",
	} {
		if got := lotLocation(in, up); got != want {
			t.Errorf("lotLocation(%q) = %q, want %q", in, got, want)
		}
	}
}
