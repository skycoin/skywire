// Package api pkg/transport-setup/api/api_test.go — unit tests for the
// transport-setup HTTP handlers, request validation, error helpers, the pure
// dmsgServicePKs parser, and the dmsg RPC gateway's non-dial methods. The
// happy-path handlers dial a visor over dmsg (callVisorRPC) and are exercised by
// the e2e (internal/integration/transport_setup_test.go); here we cover
// everything reachable WITHOUT a live dmsg client — the parse/validate/error
// branches that must reject bad input before any dial.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/go-playground/validator/v10"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/logging"
)

func testAPI() *API {
	return &API{validator: validator.New(), logger: logging.MustGetLogger("tps_test")}
}

// decodeErr extracts the {"error": ...} body written by the error helpers.
func decodeErr(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()
	var e Error
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &e))
	return e.Error
}

// --- dmsgServicePKs (pure parser) -------------------------------------------

func TestDmsgServicePKs(t *testing.T) {
	pk, _ := cipher.GenerateKeyPair()

	require.Empty(t, dmsgServicePKs(""), "empty URL → no PKs")
	require.Empty(t, dmsgServicePKs("http://"+pk.Hex()+":80"), "wrong scheme → no PKs")
	require.Empty(t, dmsgServicePKs("dmsg://"), "prefix only → no PKs")
	require.Empty(t, dmsgServicePKs("dmsg://not-a-pk:80"), "malformed PK → no PKs")

	got := dmsgServicePKs("dmsg://" + pk.Hex() + ":80")
	require.Equal(t, cipher.PubKeys{pk}, got, "valid dmsg URL → the embedded PK")
}

// --- POST /add validation ----------------------------------------------------

func TestAddTransport_Rejects(t *testing.T) {
	pkA, _ := cipher.GenerateKeyPair()
	pkB, _ := cipher.GenerateKeyPair()

	cases := []struct {
		name, body, wantErrSub string
	}{
		{"malformed json", `{`, ""},
		{"missing to", fmt.Sprintf(`{"from":"%s","type":"stcpr"}`, pkA.Hex()), ""},
		{"missing type", fmt.Sprintf(`{"from":"%s","to":"%s"}`, pkA.Hex(), pkB.Hex()), ""},
		{"unknown field", fmt.Sprintf(`{"from":"%s","to":"%s","type":"stcpr","x":1}`, pkA.Hex(), pkB.Hex()), ""},
		// from == to must be rejected BEFORE any dmsg dial (regression guard for
		// the missing-return bug: without the return it fell through to a nil
		// dmsgC dial and panicked / double-wrote the response).
		{"same from and to", fmt.Sprintf(`{"from":"%s","to":"%s","type":"stcpr"}`, pkA.Hex(), pkA.Hex()), "source and destination keys are the same"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			r := httptest.NewRequest(http.MethodPost, "/add", strings.NewReader(tc.body))
			testAPI().addTransport(w, r)

			require.Equal(t, http.StatusBadRequest, w.Code)
			if tc.wantErrSub != "" {
				require.Contains(t, decodeErr(t, w), tc.wantErrSub)
			}
		})
	}
}

// --- POST /remove validation -------------------------------------------------

func TestRemoveTransport_Rejects(t *testing.T) {
	pkA, _ := cipher.GenerateKeyPair()

	cases := []struct{ name, body string }{
		{"malformed json", `{`},
		{"missing id", fmt.Sprintf(`{"from":"%s"}`, pkA.Hex())},
		{"missing from", `{"id":"123e4567-e89b-12d3-a456-426614174000"}`},
		{"unknown field", fmt.Sprintf(`{"from":"%s","id":"123e4567-e89b-12d3-a456-426614174000","x":1}`, pkA.Hex())},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			r := httptest.NewRequest(http.MethodPost, "/remove", strings.NewReader(tc.body))
			testAPI().removeTransport(w, r)
			require.Equal(t, http.StatusBadRequest, w.Code)
		})
	}
}

// --- GET /{pk}/transports bad PK param --------------------------------------

func TestGetTransports_BadPK(t *testing.T) {
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/not-a-pk/transports", nil)
	// Inject the chi URL param the handler reads via chi.URLParam.
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("pk", "not-a-pk")
	r = r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))

	testAPI().getTransports(w, r)
	// A bad PK must be rejected before the dmsg dial (regression guard for the
	// second missing-return bug).
	require.Equal(t, http.StatusBadRequest, w.Code)
}

// --- error helpers -----------------------------------------------------------

func TestErrorHelpers(t *testing.T) {
	api := testAPI()

	w := httptest.NewRecorder()
	api.badRequest(w, httptest.NewRequest(http.MethodGet, "/", nil), errors.New("bad input"))
	require.Equal(t, http.StatusBadRequest, w.Code)
	require.Equal(t, "bad input", decodeErr(t, w))

	w = httptest.NewRecorder()
	api.internalError(w, httptest.NewRequest(http.MethodGet, "/", nil), errors.New("boom"))
	require.Equal(t, http.StatusInternalServerError, w.Code)
	require.Equal(t, "boom", decodeErr(t, w))
}

// --- dmsg RPC gateway (non-dial methods) ------------------------------------

func TestSetupRPCGateway_HealthCheck(t *testing.T) {
	gw := &SetupRPCGateway{log: logging.MustGetLogger("gw_test")}
	var reply HealthCheckReply
	require.NoError(t, gw.HealthCheck(&HealthCheckArgs{}, &reply))
	require.Equal(t, "OK", reply.Status)
}

func TestSetupRPCGateway_AddTransport_SameKey(t *testing.T) {
	gw := &SetupRPCGateway{log: logging.MustGetLogger("gw_test")}
	pk, _ := cipher.GenerateKeyPair()
	// TargetPK == RemotePK is rejected before callVisorRPC, so no dmsg client
	// (g.api) is dereferenced.
	err := gw.AddTransport(&TransportSetupRequest{TargetPK: pk, RemotePK: pk, Type: "stcpr"}, &TransportSetupResponse{})
	require.Error(t, err)
	require.Contains(t, err.Error(), "source and destination keys are the same")
}

func TestSetupRPCGateway_RemoveTransport_Blocked(t *testing.T) {
	gw := &SetupRPCGateway{log: logging.MustGetLogger("gw_test")}
	err := gw.RemoveTransport(&RemoveTransportRequest{}, &struct{}{})
	require.ErrorIs(t, err, ErrRemoteRemovalNotAllowed)
}
