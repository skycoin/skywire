// Package visor pkg/visor/api_tpd_perkey_subscriber_test.go c3-vis-core
package visor

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/skycoin/skywire/pkg/cxo/cxoutils"
	tpdapi "github.com/skycoin/skywire/pkg/deployment/tpd/api"
)

// The reader hands back the per-key-stats HTTP body, stamp removed, so a
// caller of the endpoint can take it unchanged.
func TestReadPerKeyLeafServesHTTPShape(t *testing.T) {
	keys := map[string]map[string]int{
		"02aa": {"stcpr": 2, "total": 2},
		"02bb": {"sudph": 1, "stcpr": 1, "total": 2},
	}
	raw, err := json.Marshal(tpdapi.PerKeyStats{Keys: keys, Total: 2, Complete: true, Confidence: tpdapi.ConfidenceSettled})
	if err != nil {
		t.Fatal(err)
	}
	at := time.Now()
	snap := fakeStatsSnapshot{bodies: map[string][]byte{tpdapi.PerKeyPath: cxoutils.Gzip(raw)}, at: at}

	body, ts, ok := readPerKeyLeaf(snap)
	if !ok || !ts.Equal(at) {
		t.Fatalf("readPerKeyLeaf = ok %v ts %v, want a hit at %v", ok, ts, at)
	}
	var got map[string]map[string]int
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("body is not the per-key-stats shape: %v (%s)", err, body)
	}
	if !reflect.DeepEqual(got, keys) {
		t.Fatalf("got %v, want %v", got, keys)
	}
}

func TestReadPerKeyLeafMisses(t *testing.T) {
	empty, err := json.Marshal(tpdapi.PerKeyStats{Keys: map[string]map[string]int{}})
	if err != nil {
		t.Fatal(err)
	}
	for name, bodies := range map[string]map[string][]byte{
		"absent":    {},
		"not json":  {tpdapi.PerKeyPath: []byte("nope")},
		"empty set": {tpdapi.PerKeyPath: cxoutils.Gzip(empty)},
	} {
		if _, _, ok := readPerKeyLeaf(fakeStatsSnapshot{bodies: bodies, at: time.Now()}); ok {
			t.Fatalf("%s: want a miss", name)
		}
	}
}
