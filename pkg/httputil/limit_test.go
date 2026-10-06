// Package httputil pkg/httputil/limit_test.go c0-com-util
package httputil

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestLimitBody(t *testing.T) {
	const limit = 16
	tests := []struct {
		name    string
		body    string
		chunked bool
		want    int
	}{
		{"empty", "", false, http.StatusOK},
		{"at limit", strings.Repeat("a", limit), false, http.StatusOK},
		{"declared over limit", strings.Repeat("a", limit+1), false, http.StatusRequestEntityTooLarge},
		{"chunked at limit", strings.Repeat("a", limit), true, http.StatusOK},
		{"chunked over limit", strings.Repeat("a", limit+1), true, http.StatusRequestEntityTooLarge},
	}
	h := LimitBody(limit)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := io.ReadAll(r.Body); err != nil {
			if !BodyTooLarge(err) {
				t.Errorf("read error %v is not a MaxBytesError", err)
			}
			w.WriteHeader(http.StatusRequestEntityTooLarge)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(tc.body))
			if tc.chunked {
				r.ContentLength = -1
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != tc.want {
				t.Fatalf("status %d, want %d", w.Code, tc.want)
			}
		})
	}
}

func TestLimitBodyNilBody(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Body = nil
	w := httptest.NewRecorder()
	LimitBody(1)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Body != nil {
			t.Error("nil body was replaced")
		}
	})).ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d, want 200", w.Code)
	}
}
