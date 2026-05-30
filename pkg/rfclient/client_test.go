package rfclient

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/routing"
)

func testEdges(t *testing.T) routing.PathEdges {
	t.Helper()
	pk1, _ := cipher.GenerateKeyPair()
	pk2, _ := cipher.GenerateKeyPair()
	return routing.PathEdges{pk1, pk2}
}

func TestSanitizedAddr(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "empty defaults to localhost", in: "", want: "http://localhost"},
		{name: "adds missing scheme", in: "example.com", want: "http://example.com"},
		{name: "adds scheme to scheme-relative addr", in: "//rf.example.com:8080", want: "http://rf.example.com:8080"},
		{name: "keeps existing scheme", in: "https://rf.example.com", want: "https://rf.example.com"},
		{name: "trims trailing slash", in: "http://example.com/", want: "http://example.com"},
		{name: "adds scheme and trims trailing slash", in: "example.com/api/", want: "http://example.com/api"},
		{name: "parse error falls back to localhost", in: "http://\x7f", want: "http://localhost"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := sanitizedAddr(tc.in); got != tc.want {
				t.Errorf("sanitizedAddr(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestNewHTTP(t *testing.T) {
	t.Run("zero timeout uses default and nil logger is handled", func(t *testing.T) {
		c := NewHTTP("example.com/", 0, &http.Client{}, nil).(*apiClient)
		if c.apiTimeout != defaultContextTimeout {
			t.Errorf("apiTimeout = %v, want default %v", c.apiTimeout, defaultContextTimeout)
		}
		if c.addr != "http://example.com" {
			t.Errorf("addr = %q, want sanitized http://example.com", c.addr)
		}
		if c.log == nil {
			t.Error("log is nil")
		}
	})

	t.Run("explicit timeout and master logger are used", func(t *testing.T) {
		c := NewHTTP("http://rf", 3*time.Second, &http.Client{}, logging.NewMasterLogger()).(*apiClient)
		if c.apiTimeout != 3*time.Second {
			t.Errorf("apiTimeout = %v, want 3s", c.apiTimeout)
		}
		if c.log == nil {
			t.Error("log is nil")
		}
	})
}

func TestFindRoutes_Success(t *testing.T) {
	edges := testEdges(t)
	want := map[routing.PathEdges][][]routing.Hop{
		edges: {
			{
				{TpID: uuid.New(), From: edges[0], To: edges[1]},
			},
		},
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/routes" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		if ct := r.Header.Get("Content-Type"); ct != "application/json" {
			t.Errorf("Content-Type = %q, want application/json", ct)
		}
		// The request body must be valid FindRoutesRequest JSON.
		var req FindRoutesRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("server failed to decode request body: %v", err)
		}
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(want)
	}))
	defer srv.Close()

	c := NewHTTP(srv.URL, time.Second, srv.Client(), nil)
	got, err := c.FindRoutes(context.Background(), []routing.PathEdges{edges}, &RouteOptions{MinHops: 1, MaxHops: 3})
	if err != nil {
		t.Fatalf("FindRoutes: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("FindRoutes result mismatch\n got: %+v\nwant: %+v", got, want)
	}
}

func TestFindRoutes_NotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	c := NewHTTP(srv.URL, time.Second, srv.Client(), nil)
	_, err := c.FindRoutes(context.Background(), nil, nil)
	if !errors.Is(err, ErrTransportNotFound) {
		t.Fatalf("err = %v, want ErrTransportNotFound", err)
	}
}

func TestFindRoutes_ErrorStatusWithJSONBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(HTTPResponse{Error: &HTTPError{Message: "bad edges", Code: http.StatusBadRequest}})
	}))
	defer srv.Close()

	c := NewHTTP(srv.URL, time.Second, srv.Client(), nil)
	_, err := c.FindRoutes(context.Background(), nil, nil)
	if err == nil {
		t.Fatal("expected error for non-200 status")
	}
	if got := err.Error(); !strings.Contains(got, "bad edges") || !strings.Contains(got, "400") {
		t.Errorf("error = %q, want it to mention the API message and status", got)
	}
}

func TestFindRoutes_ErrorStatusWithUndecodableBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("this is not json"))
	}))
	defer srv.Close()

	c := NewHTTP(srv.URL, time.Second, srv.Client(), nil)
	_, err := c.FindRoutes(context.Background(), nil, nil)
	if err == nil {
		t.Fatal("expected error for non-200 status")
	}
	// Falls back to HTTP status text when the body can't be decoded.
	if got := err.Error(); !strings.Contains(got, "Internal Server Error") || !strings.Contains(got, "500") {
		t.Errorf("error = %q, want fallback status-text message", got)
	}
}

func TestFindRoutes_UndecodableSuccessBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("{not valid json"))
	}))
	defer srv.Close()

	c := NewHTTP(srv.URL, time.Second, srv.Client(), nil)
	_, err := c.FindRoutes(context.Background(), nil, nil)
	if err == nil {
		t.Fatal("expected decode error for malformed 200 body")
	}
}

// errRoundTripper makes client.Do fail without a network round-trip.
type errRoundTripper struct{}

func (errRoundTripper) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, errors.New("transport boom")
}

func TestFindRoutes_DoError(t *testing.T) {
	c := NewHTTP("http://rf.invalid", time.Second, &http.Client{Transport: errRoundTripper{}}, nil)
	_, err := c.FindRoutes(context.Background(), nil, nil)
	if err == nil {
		t.Fatal("expected error when client.Do fails")
	}
	if !strings.Contains(err.Error(), "transport boom") {
		t.Errorf("error = %q, want it to surface the transport error", err.Error())
	}
}

func TestFindRoutes_RequestBuildError(t *testing.T) {
	// A control character in the address makes http.NewRequest fail. Construct
	// the client directly to bypass sanitizedAddr (which would rewrite it).
	c := &apiClient{
		addr:       "http://\x7f",
		client:     &http.Client{},
		apiTimeout: time.Second,
		log:        logging.MustGetLogger("test"),
	}
	_, err := c.FindRoutes(context.Background(), nil, nil)
	if err == nil {
		t.Fatal("expected http.NewRequest to fail on an invalid address")
	}
}
