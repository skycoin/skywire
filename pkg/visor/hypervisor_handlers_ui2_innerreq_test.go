package visor

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/httputil"
)

// A browser request asks for gzip. The in-process call must still get JSON
// back from a compressing handler such as tpviz.
func TestInnerReqCompressedHandler(t *testing.T) {
	big := map[string]string{"k": strings.Repeat("x", 4*httputil.CompressMinBytes)}
	h := httputil.CompressMin(httputil.CompressMinBytes, 5)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(big) //nolint:errcheck,gosec
	}))
	r := httptest.NewRequest(http.MethodGet, "/visors/x/vpn/servers", nil)
	r.Header.Set("Accept-Encoding", "gzip, deflate, br")

	cw := &captureWriter{h: http.Header{}, code: http.StatusOK}
	h.ServeHTTP(cw, innerReq(r))
	var got map[string]string
	require.NoError(t, json.Unmarshal(cw.body.Bytes(), &got))
	require.Equal(t, big, got)
	require.Equal(t, "gzip, deflate, br", r.Header.Get("Accept-Encoding"))
}
