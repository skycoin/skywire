// Package netview pkg/visor/netview/netview_test.go c3-vis-core
package netview

import (
	"errors"
	"testing"
)

// fakeFetch answers the four requests Compute makes from canned bodies and
// records which paths were asked for.
func fakeFetch(bodies map[string]string, asked *[]string) func(service, path string) ([]byte, error) {
	return func(service, path string) ([]byte, error) {
		key := service + " " + path
		*asked = append(*asked, key)
		if b, ok := bodies[key]; ok {
			return []byte(b), nil
		}
		return nil, errors.New("not found")
	}
}

func TestComputeCountsFromPerKeyStats(t *testing.T) {
	const (
		pkA = "02aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
		pkB = "02bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
		pkC = "02cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
	)
	var asked []string
	fetch := fakeFetch(map[string]string{
		"sd /api/services?type=visor": `[{"address":"` + pkA + `:44","geo":{"country":"DE"},"version":"v1.3.99"}]`,
		"sd /api/services?type=proxy": `[{"address":"` + pkA + `:3","geo":{"country":"DE"}}]`,
		"tpd /uptimes?v=v2":           `[{"pk":"` + pkA + `","on":true},{"pk":"` + pkB + `","on":false}]`,
		// Legacy names fold into their canonical columns; a type with no
		// column ("sudpr") still counts toward TPD's total.
		"tpd /all-transports/per-key-stats": `{
			"` + pkA + `": {"stcpr": 2, "quic": 1, "squicr": 1, "ws": 1, "wt": 1, "total": 6},
			"` + pkB + `": {"sudph": 3, "dmsg": 1, "sudpr": 1, "total": 5},
			"` + pkC + `": {"stcpr": 1, "total": 1}
		}`,
	}, &asked)

	resp := Compute(fetch)

	for _, k := range asked {
		if k == "tpd /all-transports" {
			t.Fatal("Compute downloaded the whole transport list")
		}
	}
	if len(resp.Entries) != 3 {
		t.Fatalf("want 3 rows, got %d: %+v", len(resp.Entries), resp.Entries)
	}
	a, b, c := resp.Entries[0], resp.Entries[1], resp.Entries[2]
	if a.PK != pkA+":44" || a.Services != "proxy,visor" || a.Country != "DE" || a.UTStatus != "online" {
		t.Fatalf("row A identity wrong: %+v", a)
	}
	if a.STCPR != 2 || a.SQUICR != 2 || a.SWSR != 1 || a.SWTR != 1 || a.Total != 6 {
		t.Fatalf("row A counts wrong: %+v", a)
	}
	if b.PK != pkB || b.SUDPH != 3 || b.DMSG != 1 || b.Total != 5 || b.UTStatus != "offline" {
		t.Fatalf("row B wrong: %+v", b)
	}
	if c.PK != pkC || c.STCPR != 1 || c.Total != 1 || c.UTStatus != "" {
		t.Fatalf("row C wrong: %+v", c)
	}
}

// Most transports first, PK breaking ties.
func TestComputeSortsByTotalThenPK(t *testing.T) {
	var asked []string
	resp := Compute(fakeFetch(map[string]string{
		"tpd /all-transports/per-key-stats": `{"03b":{"total":2},"03a":{"total":2},"03c":{"total":9}}`,
	}, &asked))
	var got []string
	for _, e := range resp.Entries {
		got = append(got, e.PK)
	}
	want := []string{"03c", "03a", "03b"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}
