package visor

import (
	"encoding/json"
	"testing"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/cxo/cxoutils"
	dmsgdisc "github.com/skycoin/skywire/pkg/dmsg/disc"
)

// TestDecodeClientsBatch proves the batched per-server leaf body decodes
// back to its client entries, and that framing/version errors degrade to
// an empty result (caller then falls back to HTTP).
func TestDecodeClientsBatch(t *testing.T) {
	srv, _ := cipher.GenerateKeyPair()
	c1, _ := cipher.GenerateKeyPair()
	c2, _ := cipher.GenerateKeyPair()
	entries := []*dmsgdisc.Entry{
		{Version: "0.0.1", Static: c1, Client: &dmsgdisc.Client{DelegatedServers: []cipher.PubKey{srv}}},
		{Version: "0.0.1", Static: c2, Client: &dmsgdisc.Client{DelegatedServers: []cipher.PubKey{srv}}},
	}
	payload, err := json.Marshal(entries)
	if err != nil {
		t.Fatal(err)
	}

	// Correct version → full decode.
	blob := cxoutils.FrameGzip(clientsByServerBatchVersion, payload)
	got := decodeClientsBatch(blob)
	if len(got) != 2 {
		t.Fatalf("decoded %d entries, want 2", len(got))
	}
	if got[0].Static != c1 || got[1].Static != c2 {
		t.Fatal("decoded entries lost their client PKs")
	}

	// Wrong version byte → skipped (empty), so the resolver falls back.
	bad := cxoutils.FrameGzip(clientsByServerBatchVersion+1, payload)
	if n := len(decodeClientsBatch(bad)); n != 0 {
		t.Fatalf("wrong-version blob decoded %d entries, want 0", n)
	}

	// Garbage body → skipped, not a panic.
	if n := len(decodeClientsBatch([]byte{clientsByServerBatchVersion, 0x00, 0x01})); n != 0 {
		t.Fatalf("garbage blob decoded %d entries, want 0", n)
	}
	if n := len(decodeClientsBatch(nil)); n != 0 {
		t.Fatalf("nil blob decoded %d entries, want 0", n)
	}
}

// TestLegacyPerItemLeafStillParses pins that a legacy single-entry leaf
// body (raw JSON disc.Entry, the pre-batch shape) still unmarshals, so an
// upgraded reader interoperates with a not-yet-upgraded publisher.
func TestLegacyPerItemLeafStillParses(t *testing.T) {
	c1, _ := cipher.GenerateKeyPair()
	srv, _ := cipher.GenerateKeyPair()
	e := &dmsgdisc.Entry{Version: "0.0.1", Static: c1, Client: &dmsgdisc.Client{DelegatedServers: []cipher.PubKey{srv}}}
	body, err := json.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	var got dmsgdisc.Entry
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("legacy per-item leaf failed to parse: %v", err)
	}
	if got.Static != c1 {
		t.Fatal("legacy leaf lost client PK")
	}
}
