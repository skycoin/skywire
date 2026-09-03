//go:build !js

package spec

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestLookupCXO_RoundTrip verifies the visor-wide lookup_cxo flag
// survives Marshal → Unmarshal in the single-object shape and is
// omitted when false.
func TestLookupCXO_RoundTrip(t *testing.T) {
	var c DmsgConfig
	if err := json.Unmarshal([]byte(`{"discovery":"http://disc.example","lookup_cxo":true}`), &c); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !c.LookupCXO {
		t.Fatalf("LookupCXO not parsed from JSON")
	}

	out, err := json.Marshal(c)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(out), `"lookup_cxo":true`) {
		t.Fatalf("LookupCXO not emitted: %s", out)
	}

	// Default false is omitted.
	c.LookupCXO = false
	out, err = json.Marshal(c)
	if err != nil {
		t.Fatalf("marshal default: %v", err)
	}
	if strings.Contains(string(out), "lookup_cxo") {
		t.Fatalf("LookupCXO false should be omitted: %s", out)
	}
}
