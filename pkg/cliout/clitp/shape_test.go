package clitp

import (
	"encoding/json"
	"strings"
	"testing"
)

// The e2e suite unmarshals this shape (internal/integration/stcp_test.go's
// stcpTpView) and indexes byte counters out of it, so the field names are a
// contract, not an implementation detail. This pins them.
func TestJSONFieldNames(t *testing.T) {
	b, err := json.Marshal(Transport{})
	if err != nil {
		t.Fatal(err)
	}
	// Everything optional must vanish when unset, or a consumer cannot tell
	// "not requested" from "zero".
	if got := string(b); got != `{"type":"","id":"00000000-0000-0000-0000-000000000000","remote_pk":"000000000000000000000000000000000000000000000000000000000000000000","mode":"","label":""}` {
		t.Errorf("empty Transport marshaled as:\n  %s", got)
	}

	full, err := json.Marshal(Transport{RecvBytes: 1, SentBytes: 2, LatencyMS: 3, Inactive: true, Version: "v1", Country: "US", Services: "x"})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`"recv_bytes":1`, `"sent_bytes":2`, `"latency_ms":3`,
		`"inactive":true`, `"version":"v1"`, `"country":"US"`, `"services":"x"`,
	} {
		if !strings.Contains(string(full), want) {
			t.Errorf("missing %s in:\n  %s", want, full)
		}
	}
}
