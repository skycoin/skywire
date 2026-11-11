// Package visor pkg/visor/csrf_guard_test.go c3-vis-core
package visor

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCSRFGuard(t *testing.T) {
	token, err := newCSRFToken()
	require.NoError(t, err)

	tests := []struct {
		name   string
		method string
		header map[string]string
		want   int
	}{
		{"cross-site write without token", http.MethodPost, map[string]string{"Sec-Fetch-Site": "cross-site"}, http.StatusForbidden},
		{"cross-origin write by Origin", http.MethodPut, map[string]string{"Origin": "https://evil.example"}, http.StatusForbidden},
		{"cross-site write with bad token", http.MethodDelete, map[string]string{"Sec-Fetch-Site": "cross-site", CSRFHeaderName: "bad.token"}, http.StatusForbidden},
		{"cross-site write with token", http.MethodPost, map[string]string{"Sec-Fetch-Site": "cross-site", CSRFHeaderName: token}, http.StatusOK},
		{"same-origin write", http.MethodPost, map[string]string{"Sec-Fetch-Site": "same-origin"}, http.StatusOK},
		{"typed-URL write", http.MethodPost, map[string]string{"Sec-Fetch-Site": "none"}, http.StatusOK},
		{"same-origin write by Origin", http.MethodPost, map[string]string{"Origin": "http://127.0.0.1:8000"}, http.StatusOK},
		{"non-browser write", http.MethodPost, nil, http.StatusOK},
		{"cross-site read", http.MethodGet, map[string]string{"Sec-Fetch-Site": "cross-site"}, http.StatusOK},
	}
	hv := &Hypervisor{}
	h := hv.csrfGuard(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }))
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(tc.method, "http://127.0.0.1:8000/api/network", nil)
			for k, v := range tc.header {
				r.Header.Set(k, v)
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			require.Equal(t, tc.want, w.Code)
		})
	}
}
