package visor

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestServiceFetchOrder pins the transport-preference rule for service data
// fetches: DMSG-HTTP is the default service transport, so it is tried first
// whenever a dmsg URL is configured; plain HTTP is only the dual-config
// fallback or the http-only path. Regression guard for the blank hypervisor
// "network" tab — a dmsg-only visor (no HTTP service_discovery URL) must still
// reach service-discovery over dmsg instead of failing "not configured".
func TestServiceFetchOrder(t *testing.T) {
	const httpURL = "http://sd.example"
	const dmsgURL = "dmsg://0204890f:80"

	tests := []struct {
		name             string
		httpURL, dmsgURL string
		want             []serviceHop
	}{
		{
			name:    "dual: dmsg first then http",
			httpURL: httpURL, dmsgURL: dmsgURL,
			want: []serviceHop{{dmsg: true, baseURL: dmsgURL}, {dmsg: false, baseURL: httpURL}},
		},
		{
			name:    "dmsg-only: dmsg only (the default config)",
			httpURL: "", dmsgURL: dmsgURL,
			want: []serviceHop{{dmsg: true, baseURL: dmsgURL}},
		},
		{
			name:    "http-only: http only",
			httpURL: httpURL, dmsgURL: "",
			want: []serviceHop{{dmsg: false, baseURL: httpURL}},
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
