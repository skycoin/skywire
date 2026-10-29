// Package launcher pkg/app/launcher/registry_test.go
package launcher

import (
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"testing"

	"github.com/spf13/pflag"
)

// TestHTTPHandlerRegistry covers the in-process HTTP-handler registry that
// backs portless-internal apps (skychat): register → get → serve in-process →
// clear. This is the seam the visor's control surface uses instead of a
// loopback dial when an app runs with no TCP port of its own.
func TestHTTPHandlerRegistry(t *testing.T) {
	const name = "test-portless-app"

	if h := GetHTTPHandler(name); h != nil {
		t.Fatalf("expected no handler before registration, got %v", h)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/status", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`)) //nolint:errcheck
	})
	RegisterHTTPHandler(name, mux)

	h := GetHTTPHandler(name)
	if h == nil {
		t.Fatal("expected handler after registration, got nil")
	}

	// Serve in-process, the way Visor.SkychatProxy does.
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/status", nil)
	h.ServeHTTP(rec, req)
	res := rec.Result()
	body, _ := io.ReadAll(res.Body) //nolint:errcheck // test read; error not material
	_ = res.Body.Close()            //nolint:errcheck
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", res.StatusCode)
	}
	if got, want := string(body), `{"ok":true}`; got != want {
		t.Fatalf("body = %q, want %q", got, want)
	}

	// Re-registration overwrites (an app restart re-publishes).
	RegisterHTTPHandler(name, http.NewServeMux())
	if GetHTTPHandler(name) == nil {
		t.Fatal("expected handler after re-registration")
	}

	// nil clears (shutdown).
	RegisterHTTPHandler(name, nil)
	if h := GetHTTPHandler(name); h != nil {
		t.Fatalf("expected handler cleared after nil registration, got %v", h)
	}
}

func TestRegisteredAppsSorted(t *testing.T) {
	RegisterApp("zz-catalog-test", nil)
	RegisterApp("aa-catalog-test", nil)
	names := RegisteredApps()
	if !sort.StringsAreSorted(names) {
		t.Fatalf("names not sorted: %v", names)
	}
	var a, z bool
	for _, n := range names {
		a = a || n == "aa-catalog-test"
		z = z || n == "zz-catalog-test"
	}
	if !a || !z {
		t.Fatalf("registered names missing from %v", names)
	}
}

func TestAppMetadata(t *testing.T) {
	for name, info := range appInfo {
		switch info.Kind {
		case KindServer, KindClient, KindService:
		default:
			t.Errorf("%s: kind %q", name, info.Kind)
		}
		if info.Description == "" {
			t.Errorf("%s: no description", name)
		}
	}
	if info, ok := AppMetadata("skysocks-client"); !ok || info.Kind != KindClient || !info.MultiInstance {
		t.Errorf("skysocks-client: %+v %v", info, ok)
	}
	if info, ok := AppMetadata("resolver-extra"); !ok || info.Kind != KindService {
		t.Errorf("resolver-extra: %+v %v", info, ok)
	}
	if _, ok := AppMetadata("no-such-app"); ok {
		t.Error("unknown app has metadata")
	}
}

func TestAppFlagsRegistry(t *testing.T) {
	fs := pflag.NewFlagSet("flags-test", pflag.ContinueOnError)
	fs.String("b", "", "")
	fs.Bool("a", false, "")
	RegisterAppFlags("flags-test", fs)

	var names []string
	if !VisitAppFlags("flags-test", func(f *pflag.Flag) { names = append(names, f.Name) }) {
		t.Fatal("registered flag set not found")
	}
	if len(names) != 2 || names[0] != "a" || names[1] != "b" {
		t.Errorf("flags = %v, want [a b]", names)
	}
	if VisitAppFlags("no-such-app", func(*pflag.Flag) {}) {
		t.Error("unregistered app reported a flag set")
	}
}
