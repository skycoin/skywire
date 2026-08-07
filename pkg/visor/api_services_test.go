package visor

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestServiceFetchOrder pins the dmsg-only fetch rule: only dmsg:// endpoints
// are used (plain HTTP to deployment services is no longer supported), and the
// dmsg URL is taken from whichever field holds it — the dmsg-only default config
// stores it in the "http" field (service_discovery = "dmsg://..."), legacy dual
// configs use the *_dmsg field. Clearnet URLs are dropped entirely.
func TestServiceFetchOrder(t *testing.T) {
	const httpURL = "http://sd.example"
	const dmsgURL = "dmsg://0204890f:80"

	tests := []struct {
		name             string
		httpURL, dmsgURL string
		want             []serviceHop
	}{
		{
			name:    "dual (http + dmsg): only dmsg is used, clearnet dropped",
			httpURL: httpURL, dmsgURL: dmsgURL,
			want: []serviceHop{{baseURL: dmsgURL}},
		},
		{
			name:    "dmsg-only via the dmsg field",
			httpURL: "", dmsgURL: dmsgURL,
			want: []serviceHop{{baseURL: dmsgURL}},
		},
		{
			name:    "dmsg-only default: dmsg URL stored in the http field",
			httpURL: dmsgURL, dmsgURL: "",
			want: []serviceHop{{baseURL: dmsgURL}},
		},
		{
			name:    "clearnet-only: dropped (plain HTTP unsupported)",
			httpURL: httpURL, dmsgURL: "",
			want: nil,
		},
		{
			name:    "neither configured",
			httpURL: "", dmsgURL: "",
			want: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, serviceFetchOrder(tt.httpURL, tt.dmsgURL))
		})
	}
}

// TestDoHealthProbe_BuildInfoVersion asserts the shared health probe extracts
// the service version from build_info.version — the field path the DMSG Server
// rows now rely on. They route their version fetch through doHealthProbe (the
// same discovery-routed extraction as the deployment-service rows) instead of
// the old session-pinned dmsg stream, which never reached the server's
// transit-client /health and left the VERSION column empty.
func TestDoHealthProbe_BuildInfoVersion(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/health", r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"build_info":{"version":"v1.3.92-0-7925d659e2ec"}}`)) //nolint
	}))
	defer srv.Close()

	entry := doHealthProbe(srv.Client(), "DMSG Server", srv.URL, "dmsg")
	require.Equal(t, "OK", entry.Status)
	require.Equal(t, "v1.3.92-0-7925d659e2ec", entry.Version)
}

// TestDoHealthProbe_TopLevelVersionFallback asserts the fallback to a top-level
// "version" field when build_info is absent.
func TestDoHealthProbe_TopLevelVersionFallback(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"version":"v1.3.88-0-54e4b7f90c01"}`)) //nolint
	}))
	defer srv.Close()

	entry := doHealthProbe(srv.Client(), "DMSG Server", srv.URL, "dmsg")
	require.Equal(t, "OK", entry.Status)
	require.Equal(t, "v1.3.88-0-54e4b7f90c01", entry.Version)
}
