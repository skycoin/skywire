package charts

import (
	"compress/gzip"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// A client that takes gzip gets the page compressed once per render, and the
// same document a plain client gets.
func TestPageServesCachedGzip(t *testing.T) {
	builds := 0
	p := &Page{
		Title: "t",
		Build: func(context.Context, Range, time.Time) (Content, error) {
			builds++
			return Content{}, nil
		},
	}
	get := func(gz bool) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		if gz {
			req.Header.Set("Accept-Encoding", "gzip")
		}
		rec := httptest.NewRecorder()
		p.ServeHTTP(rec, req)
		return rec
	}

	plain := get(false)
	if plain.Header().Get("Content-Encoding") != "" {
		t.Fatal("plain client got an encoded body")
	}
	for i := 0; i < 2; i++ {
		rec := get(true)
		if rec.Header().Get("Content-Encoding") != "gzip" {
			t.Fatalf("request %d: not gzipped", i)
		}
		zr, err := gzip.NewReader(rec.Body)
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(zr)
		if err != nil {
			t.Fatal(err)
		}
		if string(body) != plain.Body.String() {
			t.Fatal("gzipped body differs from the plain one")
		}
	}
	if builds != 1 {
		t.Fatalf("%d builds, want 1", builds)
	}
}
