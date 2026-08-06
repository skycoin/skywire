package wasmhv

import (
	"encoding/json"
	"testing"

	"github.com/google/uuid"

	"github.com/skycoin/skywire/pkg/cipher"
)

// fakeSelf is a SelfProvider stub for a known local visor.
type fakeSelf struct {
	pk         cipher.PubKey
	tps        []*TransportSummary
	sinceSink  *int64  // records the last SelfRuntimeLogs(since) arg, when non-nil
	setCfgSink *[]byte // records the last SelfSetRuntimeConfig(body) arg, when non-nil
}

func (f fakeSelf) SelfPK() cipher.PubKey { return f.pk }
func (f fakeSelf) SelfOverview() Overview {
	return Overview{PubKey: f.pk, RoutesCount: 3}
}
func (f fakeSelf) SelfSummary() Summary {
	ov := f.SelfOverview()
	return Summary{Overview: &ov, Online: true, IsHypervisor: true}
}
func (f fakeSelf) SelfTransports() []*TransportSummary { return f.tps }
func (f fakeSelf) SelfRoutes() []byte                  { return []byte("[]") }
func (f fakeSelf) SelfNetworkView() []byte             { return nil }
func (f fakeSelf) SelfNetworkTransports(int) []byte    { return nil }
func (f fakeSelf) SelfServiceHealth() []byte           { return nil }
func (f fakeSelf) SelfDmsgSessions() []byte {
	return []byte(`{"main":{"pk":"","role":"main","count":1,"servers":[]}}`)
}
func (f fakeSelf) SelfDmsgConnectAll() []byte {
	return []byte(`{"total":0,"already_connected":0,"newly_connected":0}`)
}
func (f fakeSelf) SelfRouterSettings() []byte {
	return []byte(`{"force_local_routes":false,"existing_tp_only":false,"mux_routes":0,"min_hops":0}`)
}
func (f fakeSelf) SelfRuntimeConfig() []byte { return []byte(`{"version":"test"}`) }
func (f fakeSelf) SelfSetRuntimeConfig(body []byte) (int, []byte) {
	if f.setCfgSink != nil {
		*f.setCfgSink = body
	}
	return 200, []byte(`{"ok":true}`)
}
func (f fakeSelf) SelfRuntimeLogs(since int64) []byte {
	if f.sinceSink != nil {
		*f.sinceSink = since
	}
	return []byte(`{"entries":[],"latest":0,"dropped":0}`)
}

func (f fakeSelf) SelfApps() []byte                { return []byte("[]") }
func (f fakeSelf) StartApp(string) error           { return nil }
func (f fakeSelf) StopApp(string) error            { return nil }
func (f fakeSelf) SetAutoStart(string, bool) error { return nil }

func newSelfPK(t *testing.T) cipher.PubKey {
	t.Helper()
	pk, _ := cipher.GenerateKeyPair()
	return pk
}

// TestSelf_ListedAndRoutedLocally verifies that once a SelfProvider is attached,
// the local visor appears in the summary list and its /visors/<pk> routes are
// served from the provider (not a gob RPC dial), with no remote visors connected.
func TestSelf_ListedAndRoutedLocally(t *testing.T) {
	pk := newSelfPK(t)
	tid := uuid.New()
	core := NewCore(pk, nil)
	core.SetSelf(fakeSelf{
		pk:  pk,
		tps: []*TransportSummary{{ID: tid, Local: pk, Remote: newSelfPK(t), Type: "wt", Label: "user"}},
	})

	// /visors-summary includes the self visor (the only one).
	status, body := core.ServeHTTP("GET", "/api/visors-summary", nil)
	if status != 200 {
		t.Fatalf("visors-summary status = %d", status)
	}
	var sums []Summary
	if err := json.Unmarshal(body, &sums); err != nil {
		t.Fatalf("unmarshal summaries: %v", err)
	}
	if len(sums) != 1 || sums[0].Overview == nil || sums[0].Overview.PubKey != pk {
		t.Fatalf("self visor not first in summaries: %+v", sums)
	}
	if !sums[0].IsHypervisor || !sums[0].Online {
		t.Fatalf("self summary flags wrong: %+v", sums[0])
	}

	// /visors/<selfPK> is served locally (overview with the route count).
	status, body = core.ServeHTTP("GET", "/api/visors/"+pk.Hex(), nil)
	if status != 200 {
		t.Fatalf("self overview status = %d", status)
	}
	var ov Overview
	if err := json.Unmarshal(body, &ov); err != nil {
		t.Fatalf("unmarshal overview: %v", err)
	}
	if ov.PubKey != pk || ov.RoutesCount != 3 {
		t.Fatalf("self overview wrong: %+v", ov)
	}

	// /visors/<selfPK>/transports returns the local transports.
	status, body = core.ServeHTTP("GET", "/api/visors/"+pk.Hex()+"/transports", nil)
	if status != 200 {
		t.Fatalf("self transports status = %d", status)
	}
	var tps []*TransportSummary
	if err := json.Unmarshal(body, &tps); err != nil {
		t.Fatalf("unmarshal transports: %v", err)
	}
	if len(tps) != 1 || tps[0].ID != tid || tps[0].Type != "wt" {
		t.Fatalf("self transports wrong: %+v", tps)
	}
}

// TestSelf_AbsentWhenNotSet verifies a plain standalone HV (no SelfProvider)
// lists no visors when none have dialed in.
func TestSelf_AbsentWhenNotSet(t *testing.T) {
	core := NewCore(newSelfPK(t), nil)
	status, body := core.ServeHTTP("GET", "/api/visors-summary", nil)
	if status != 200 {
		t.Fatalf("status = %d", status)
	}
	var sums []Summary
	if err := json.Unmarshal(body, &sums); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(sums) != 0 {
		t.Fatalf("expected no visors without a self provider, got %d", len(sums))
	}
}

// TestSelf_NodePageSubroutes verifies the node-page read subroutes the HV-UI needs
// from a browser visor are served (200), not 404 — the fix for the dmsg-sessions
// "Failed to fetch" error, the router-settings / runtime-config expander errors, and
// the Logs page "subroute not implemented" error. Also checks runtime-logs threads
// the ?since= cursor through to the provider.
func TestSelf_NodePageSubroutes(t *testing.T) {
	pk := newSelfPK(t)
	var gotSince int64 = -1
	core := NewCore(pk, nil)
	core.SetSelf(fakeSelf{pk: pk, sinceSink: &gotSince})

	base := "/api/visors/" + pk.Hex()
	for _, sub := range []string{"/dmsg/sessions", "/router-settings", "/runtime-config", "/runtime-logs", "/ports", "/apps"} {
		status, body := core.ServeHTTP("GET", base+sub, nil)
		if status != 200 {
			t.Fatalf("%s status = %d (body %s)", sub, status, body)
		}
	}

	// runtime-logs must parse ?since= and pass it to the provider.
	if status, _ := core.ServeHTTP("GET", base+"/runtime-logs?since=42", nil); status != 200 {
		t.Fatalf("runtime-logs?since= status = %d", status)
	}
	if gotSince != 42 {
		t.Fatalf("runtime-logs since not threaded: got %d, want 42", gotSince)
	}
}

// TestSelf_DmsgConnectAllRoute locks the "Connect to all servers" endpoint the
// wasm core serves for the shared HV UI (it 404'd before #3632, so the button
// errored on every browser visor): POST dispatches to the provider and returns
// its result; other methods are rejected rather than silently no-oping.
func TestSelf_DmsgConnectAllRoute(t *testing.T) {
	pk := newSelfPK(t)
	core := NewCore(pk, nil)
	core.SetSelf(fakeSelf{pk: pk})

	base := "/api/visors/" + pk.Hex()
	status, body := core.ServeHTTP("POST", base+"/dmsg/connect-all", nil)
	if status != 200 {
		t.Fatalf("POST dmsg/connect-all status = %d (body %s)", status, body)
	}
	var res struct {
		Total            *int `json:"total"`
		AlreadyConnected *int `json:"already_connected"`
		NewlyConnected   *int `json:"newly_connected"`
	}
	if err := json.Unmarshal(body, &res); err != nil {
		t.Fatalf("connect-all body not the DmsgConnectAllResult shape: %v (%s)", err, body)
	}
	if res.Total == nil || res.AlreadyConnected == nil || res.NewlyConnected == nil {
		t.Fatalf("connect-all result missing fields: %s", body)
	}

	if status, _ := core.ServeHTTP("GET", base+"/dmsg/connect-all", nil); status != 405 {
		t.Fatalf("GET dmsg/connect-all status = %d, want 405", status)
	}
}

// TestSelf_RuntimeConfigPUT locks the config-editor parity: PUT /runtime-config
// dispatches to SelfSetRuntimeConfig (the browser edge's override+reload path),
// while GET still serves the config view.
func TestSelf_RuntimeConfigPUT(t *testing.T) {
	pk := newSelfPK(t)
	core := NewCore(pk, nil)
	var captured []byte
	core.SetSelf(fakeSelf{pk: pk, setCfgSink: &captured})

	base := "/api/visors/" + pk.Hex()
	edit := []byte(`{"routing":{"min_hops":2}}`)
	status, body := core.ServeHTTP("PUT", base+"/runtime-config", edit)
	if status != 200 {
		t.Fatalf("PUT runtime-config status = %d (body %s)", status, body)
	}
	if string(captured) != string(edit) {
		t.Fatalf("SelfSetRuntimeConfig got %q, want %q", captured, edit)
	}
	if st, _ := core.ServeHTTP("GET", base+"/runtime-config", nil); st != 200 {
		t.Fatalf("GET runtime-config status = %d, want 200", st)
	}
}

// TestSelf_AppLogsServesEmptyNot404 locks the fix that a self-app /logs request
// returns a valid (empty) LogsRes instead of the generic subroute 404 — so the
// Angular app-detail logs panel renders "no logs" instead of an error dialog.
func TestSelf_AppLogsServesEmptyNot404(t *testing.T) {
	pk := newSelfPK(t)
	core := NewCore(pk, nil)
	core.SetSelf(fakeSelf{pk: pk})

	status, body := core.ServeHTTP("GET", "/api/visors/"+pk.Hex()+"/apps/skychat/logs", nil)
	if status != 200 {
		t.Fatalf("apps/<app>/logs status = %d (body %s), want 200", status, body)
	}
	var res struct {
		LastLogTimestamp string   `json:"last_log_timestamp"`
		Logs             []string `json:"logs"`
	}
	if err := json.Unmarshal(body, &res); err != nil {
		t.Fatalf("app logs body not a LogsRes: %v (%s)", err, body)
	}
	if res.Logs == nil {
		t.Fatalf("logs should be an empty array, not null: %s", body)
	}
}

// TestSelf_HostStatsFlagsUnavailable locks host-stats reporting available:false
// (a browser tab can't measure its host — the UI must show N/A, not 0%).
func TestSelf_HostStatsFlagsUnavailable(t *testing.T) {
	pk := newSelfPK(t)
	core := NewCore(pk, nil)
	core.SetSelf(fakeSelf{pk: pk})

	status, body := core.ServeHTTP("GET", "/api/visors/"+pk.Hex()+"/host-stats", nil)
	if status != 200 {
		t.Fatalf("host-stats status = %d", status)
	}
	var res struct {
		Available *bool `json:"available"`
	}
	if err := json.Unmarshal(body, &res); err != nil || res.Available == nil || *res.Available {
		t.Fatalf("host-stats must report available:false, got %s", body)
	}
}
