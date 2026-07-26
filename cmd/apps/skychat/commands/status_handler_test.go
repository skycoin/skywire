// Package commands cmd/apps/skychat/commands/status_handler_test.go
//
// Coverage for the /status operator surface: it must always return 200
// with a well-formed JSON object carrying the documented health fields,
// and must degrade gracefully (groups + groups_error) when the pair RPC
// is unavailable — never panic on a nil appCl / nil pairRPC.
package commands

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/skycoin/skywire/pkg/cipher"
)

func TestStatusHandler_JSONShape(t *testing.T) {
	if appLog == nil {
		appLog = func(string, ...any) {}
	}
	origHub := hub
	hub = newSSEHub()
	connsMu.Lock()
	if conns == nil {
		conns = make(map[cipher.PubKey]*framedConn)
	}
	connsMu.Unlock()
	// Force the pair RPC unavailable so collectGroupHealth is deterministic.
	pairRPCMu.Lock()
	prevRPC := pairRPC
	pairRPC = nil
	pairRPCMu.Unlock()
	t.Cleanup(func() {
		hub = origHub
		pairRPCMu.Lock()
		pairRPC = prevRPC
		pairRPCMu.Unlock()
	})

	rr := httptest.NewRecorder()
	statusHandler(rr, httptest.NewRequest(http.MethodGet, "/status", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status code = %d, want 200; body=%q", rr.Code, rr.Body.String())
	}

	var m map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &m); err != nil {
		t.Fatalf("status body is not a JSON object: %v\nbody=%q", err, rr.Body.String())
	}

	for _, key := range []string{
		"sse_subscribers", "active_peer_conns", "peers", "persistence_enabled",
		"pairing_enabled", "frame_proto_version", "schema_version", "app_uptime_sec",
		"inbound_msg_count", "outbound_msg_count", "inbound_drop_count",
		"outbound_fail_count", "outbound_retry_count", "sse_drop_count",
		"last_rx_ts", "last_send_ts", "groups",
	} {
		if _, ok := m[key]; !ok {
			t.Errorf("status JSON missing key %q", key)
		}
	}

	if v, ok := m["frame_proto_version"].(float64); !ok || int(v) != frameProtoVersion {
		t.Errorf("frame_proto_version = %v, want %d", m["frame_proto_version"], frameProtoVersion)
	}
	if v, ok := m["schema_version"].(string); !ok || v != schemaVersion {
		t.Errorf("schema_version = %v, want %q", m["schema_version"], schemaVersion)
	}
	// With the pair RPC unavailable, collectGroupHealth reports the reason
	// while still returning an (empty) groups array — never a silent nil.
	if _, ok := m["groups"]; !ok {
		t.Error("groups key should always be present, even on the failure path")
	}
	if reason, ok := m["groups_error"].(string); !ok || reason != "pair-rpc-disabled" {
		t.Errorf("groups_error = %v, want %q", m["groups_error"], "pair-rpc-disabled")
	}
}
