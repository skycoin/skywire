//go:build !js

package spec

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestLocalRelay_RoundTrip verifies the visor-wide local_relay block survives
// Unmarshal → Marshal in the single-object shape and is omitted when unset.
//
// It is not a formality: DmsgConfig has a HAND-WRITTEN codec (spec_native.go),
// and a `json` tag on the struct field alone buys nothing — a field the codec
// does not name is dropped silently on read AND on write, which for an opt-in
// security-relevant acceptor would read to the operator as "the feature does
// not work" with nothing in the logs.
func TestLocalRelay_RoundTrip(t *testing.T) {
	const raw = `{"discovery":"http://disc.example","local_relay":{"enabled":true,"socket":"/run/skywire/relay.sock","socket_mode":"0660","allowed_keys":["024ec47420176680816e0406250e7156465e4531f5b26057c9f6297bb0303558c7"]}}`

	var c DmsgConfig
	if err := json.Unmarshal([]byte(raw), &c); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if c.LocalRelay == nil {
		t.Fatal("local_relay not parsed from JSON")
	}
	if !c.LocalRelay.Enabled {
		t.Fatal("local_relay.enabled not parsed")
	}
	if c.LocalRelay.Socket != "/run/skywire/relay.sock" {
		t.Fatalf("local_relay.socket = %q", c.LocalRelay.Socket)
	}
	if c.LocalRelay.SocketMode != "0660" {
		t.Fatalf("local_relay.socket_mode = %q", c.LocalRelay.SocketMode)
	}
	if len(c.LocalRelay.AllowedKeys) != 1 {
		t.Fatalf("local_relay.allowed_keys = %v", c.LocalRelay.AllowedKeys)
	}

	out, err := json.Marshal(c)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	for _, want := range []string{`"local_relay"`, `"enabled":true`, `"socket":"/run/skywire/relay.sock"`, `"socket_mode":"0660"`} {
		if !strings.Contains(string(out), want) {
			t.Fatalf("missing %s in %s", want, out)
		}
	}

	// Unset is omitted: a generated config carries no acceptor, and the
	// feature stays opt-in.
	c.LocalRelay = nil
	out, err = json.Marshal(c)
	if err != nil {
		t.Fatalf("marshal default: %v", err)
	}
	if strings.Contains(string(out), "local_relay") {
		t.Fatalf("nil local_relay should be omitted: %s", out)
	}
}
