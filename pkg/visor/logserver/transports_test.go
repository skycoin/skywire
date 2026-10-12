// Package logserver pkg/visor/logserver/transports_test.go c3-vis-core
package logserver

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/visor/visorconfig"
)

type fixedList struct{}

func (fixedList) TransportListBody() ([]byte, string, error) { return []byte(`{"pk":"x"}`), "v1", nil }

// The list goes only to callers that came over a transport, and a caller
// holding the current version gets 304.
func TestTransportListOnlyOverATransport(t *testing.T) {
	api := New(logging.MustGetLogger("test"), t.TempDir(), "", nil, &visorconfig.Survey{}, false)
	api.SetTransportListProvider(fixedList{})

	w := httptest.NewRecorder()
	api.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/transports", nil))
	require.Equal(t, http.StatusForbidden, w.Code, "a dmsg caller is refused")

	req := httptest.NewRequest(http.MethodGet, "/transports", nil)
	req = req.WithContext(WithOverTransport(req.Context()))
	w = httptest.NewRecorder()
	api.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, `{"pk":"x"}`, w.Body.String())
	require.Equal(t, `"v1"`, w.Header().Get("ETag"))

	req = httptest.NewRequest(http.MethodGet, "/transports", nil)
	req = req.WithContext(WithOverTransport(req.Context()))
	req.Header.Set("If-None-Match", `"v1"`)
	w = httptest.NewRecorder()
	api.ServeHTTP(w, req)
	require.Equal(t, http.StatusNotModified, w.Code)
	require.Empty(t, w.Body.String())
}
