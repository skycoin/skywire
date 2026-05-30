package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/sirupsen/logrus"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/rfclient"
	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/transport"
	"github.com/skycoin/skywire/pkg/transport-discovery/store"
)

// fakeStore is a minimal store.Store: it embeds the interface (so it satisfies
// the type) and only implements the two edge-lookup methods the graph builder
// actually calls. Any other method would panic if invoked, which is the
// desired signal that the handler reached unexpected code.
type fakeStore struct {
	store.Store
	transports map[cipher.PubKey][]*transport.Entry
	edgeErr    error // when set, GetTransportsByEdge always returns it
}

func newFakeStore() *fakeStore {
	return &fakeStore{transports: make(map[cipher.PubKey][]*transport.Entry)}
}

func (f *fakeStore) GetTransportsByEdge(_ context.Context, pk cipher.PubKey) ([]*transport.Entry, error) {
	if f.edgeErr != nil {
		return nil, f.edgeErr
	}
	return f.transports[pk], nil
}

func (f *fakeStore) GetTransportsByEdgeNoLatency(ctx context.Context, pk cipher.PubKey) ([]*transport.Entry, error) {
	return f.GetTransportsByEdge(ctx, pk)
}

// saveEntry registers a bidirectional transport between a and b.
func (f *fakeStore) saveEntry(a, b cipher.PubKey) {
	e := &transport.Entry{Edges: transport.SortEdges(a, b)}
	f.transports[a] = append(f.transports[a], e)
	f.transports[b] = append(f.transports[b], e)
}

func newPK(t *testing.T) cipher.PubKey {
	t.Helper()
	pk, _ := cipher.GenerateKeyPair()
	return pk
}

// do issues a request against the full API handler chain and returns the recorder.
func do(t *testing.T, api *API, method, target string, body io.Reader) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, target, body)
	rec := httptest.NewRecorder()
	api.ServeHTTP(rec, req)
	return rec
}

func newTestAPI(s store.Store) *API {
	return New(s, logrus.New(), false, "dmsg://test:1")
}

func postRoutes(t *testing.T, api *API, reqBody rfclient.FindRoutesRequest) *httptest.ResponseRecorder {
	t.Helper()
	b, err := json.Marshal(reqBody)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	return do(t, api, http.MethodPost, "/routes", bytes.NewReader(b))
}

func TestHealth(t *testing.T) {
	api := newTestAPI(newFakeStore())
	rec := do(t, api, http.MethodGet, "/health", nil)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var resp HealthCheckResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode health response: %v", err)
	}
	if resp.ServiceName != "route-finder" {
		t.Errorf("ServiceName = %q, want route-finder", resp.ServiceName)
	}
	if resp.DmsgAddr != "dmsg://test:1" {
		t.Errorf("DmsgAddr = %q, want dmsg://test:1", resp.DmsgAddr)
	}
	if resp.StartedAt.IsZero() {
		t.Error("StartedAt is zero")
	}
}

func TestGetPairedRoutes_Success(t *testing.T) {
	src, dst := newPK(t), newPK(t)
	s := newFakeStore()
	s.saveEntry(src, dst)
	api := newTestAPI(s)

	rec := postRoutes(t, api, rfclient.FindRoutesRequest{
		Edges: []routing.PathEdges{{src, dst}},
		Opts:  &rfclient.RouteOptions{MinHops: 0, MaxHops: 5},
	})

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}

	var routes map[routing.PathEdges][][]routing.Hop
	if err := json.Unmarshal(rec.Body.Bytes(), &routes); err != nil {
		t.Fatalf("decode routes: %v", err)
	}
	paths, ok := routes[routing.PathEdges{src, dst}]
	if !ok {
		t.Fatalf("no entry for the requested edge; got %+v", routes)
	}
	if len(paths) == 0 || len(paths[0]) != 1 {
		t.Fatalf("expected a single one-hop route, got %+v", paths)
	}
	hop := paths[0][0]
	if hop.From != src || hop.To != dst {
		t.Errorf("hop = %s->%s, want %s->%s", hop.From, hop.To, src, dst)
	}
}

// Two edges sharing the same source must reuse the cached graph (the second
// iteration takes the `_, ok := graphs[srcPK]` hit branch).
func TestGetPairedRoutes_SharedSourceReusesGraph(t *testing.T) {
	src, dst1, dst2 := newPK(t), newPK(t), newPK(t)
	s := newFakeStore()
	s.saveEntry(src, dst1)
	s.saveEntry(src, dst2)
	api := newTestAPI(s)

	rec := postRoutes(t, api, rfclient.FindRoutesRequest{
		Edges: []routing.PathEdges{{src, dst1}, {src, dst2}},
		Opts:  &rfclient.RouteOptions{MaxHops: 3},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	var routes map[routing.PathEdges][][]routing.Hop
	if err := json.Unmarshal(rec.Body.Bytes(), &routes); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(routes) != 2 {
		t.Errorf("got %d edge results, want 2", len(routes))
	}
}

// With no opts, minHops and maxHops both default to 0 (covers the
// grr.Opts == nil branch and graphDepth defaulting to 10). Note: because
// maxHops stays 0, GetRoute filters out any real (>=1 hop) route, so a direct
// edge yields 404 rather than 200 — this documents the current behavior.
func TestGetPairedRoutes_NilOpts(t *testing.T) {
	src, dst := newPK(t), newPK(t)
	s := newFakeStore()
	s.saveEntry(src, dst)
	api := newTestAPI(s)

	rec := postRoutes(t, api, rfclient.FindRoutesRequest{Edges: []routing.PathEdges{{src, dst}}})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (nil opts => maxHops 0 => no route matches); body: %s", rec.Code, rec.Body.String())
	}
}

func TestGetPairedRoutes_EmptyEdges(t *testing.T) {
	api := newTestAPI(newFakeStore())
	rec := postRoutes(t, api, rfclient.FindRoutesRequest{})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var routes map[routing.PathEdges][][]routing.Hop
	if err := json.Unmarshal(rec.Body.Bytes(), &routes); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(routes) != 0 {
		t.Errorf("expected empty routes, got %+v", routes)
	}
}

func TestGetPairedRoutes_InvalidJSON(t *testing.T) {
	api := newTestAPI(newFakeStore())
	rec := do(t, api, http.MethodPost, "/routes", bytes.NewReader([]byte("{not json")))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	// handleError wraps the error in an rfclient.HTTPResponse.
	var resp rfclient.HTTPResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode error response: %v", err)
	}
	if resp.Error == nil || resp.Error.Code != http.StatusBadRequest {
		t.Errorf("error payload = %+v, want code 400", resp.Error)
	}
}

type errReader struct{}

func (errReader) Read([]byte) (int, error) { return 0, errors.New("read boom") }

func TestGetPairedRoutes_BodyReadError(t *testing.T) {
	api := newTestAPI(newFakeStore())
	rec := do(t, api, http.MethodPost, "/routes", errReader{})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestGetPairedRoutes_TransportNotFound(t *testing.T) {
	src, dst := newPK(t), newPK(t)
	s := newFakeStore()
	s.edgeErr = store.ErrTransportNotFound
	api := newTestAPI(s)

	rec := postRoutes(t, api, rfclient.FindRoutesRequest{
		Edges: []routing.PathEdges{{src, dst}},
		Opts:  &rfclient.RouteOptions{MaxHops: 2},
	})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestGetPairedRoutes_GraphBuildError(t *testing.T) {
	src, dst := newPK(t), newPK(t)
	s := newFakeStore()
	s.edgeErr = errors.New("db exploded")
	api := newTestAPI(s)

	rec := postRoutes(t, api, rfclient.FindRoutesRequest{
		Edges: []routing.PathEdges{{src, dst}},
		Opts:  &rfclient.RouteOptions{MaxHops: 2},
	})
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500 (body: %s)", rec.Code, rec.Body.String())
	}
}

// The graph builds (src has a transport) but the destination is unreachable,
// so GetRoute fails and the handler returns 404.
func TestGetPairedRoutes_NoRoute(t *testing.T) {
	src, other, dst := newPK(t), newPK(t), newPK(t)
	s := newFakeStore()
	s.saveEntry(src, other) // src is known, but dst is isolated
	api := newTestAPI(s)

	rec := postRoutes(t, api, rfclient.FindRoutesRequest{
		Edges: []routing.PathEdges{{src, dst}},
		Opts:  &rfclient.RouteOptions{MaxHops: 5},
	})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (body: %s)", rec.Code, rec.Body.String())
	}
}

// enableMetrics=true exercises the metrics middleware registration branch in New.
func TestNew_WithMetrics(t *testing.T) {
	api := New(newFakeStore(), logrus.New(), true, "")
	if api == nil {
		t.Fatal("New returned nil")
	}
	rec := do(t, api, http.MethodGet, "/health", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
}
