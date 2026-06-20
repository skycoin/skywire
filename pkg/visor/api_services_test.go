package visor

import (
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
