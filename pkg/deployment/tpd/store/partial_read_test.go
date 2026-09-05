// Package store pkg/deployment/tpd/store/partial_read_test.go c4-net-discovery
package store

import (
	"encoding/json"
	"testing"
)

// The counts are assembled from batched MGETs over the transport index, and a
// failed batch used to be skipped with a bare `continue` — silently removing
// up to mgetBatch (10,000) transports while still returning a plausible
// number. With an index observed between roughly 4,400 and 15,800 members
// that is one or two batches, so losing one could halve the reported total,
// and a consumer had no way to distinguish it from the network shrinking.
//
// TransportSummary now carries that state. These tests pin the contract; the
// batch paths themselves need a live Redis and are exercised in the
// integration suite.
func TestTransportSummaryDefaultsToComplete(t *testing.T) {
	var s TransportSummary
	if s.Partial {
		t.Error("a zero TransportSummary must not claim to be partial")
	}
	if s.MissingBatches != 0 {
		t.Errorf("MissingBatches = %d, want 0", s.MissingBatches)
	}
}

// A partial summary must be distinguishable from a complete one carrying the
// same numbers — that is the entire point of the flag.
func TestTransportSummaryPartialIsDistinguishable(t *testing.T) {
	complete := TransportSummary{Total: 8000, ByType: map[string]int{"stcpr": 8000}}
	partial := TransportSummary{Total: 8000, ByType: map[string]int{"stcpr": 8000},
		Partial: true, MissingBatches: 1}

	if complete.Total != partial.Total {
		t.Fatal("fixture error: the two summaries should hold identical counts")
	}
	if complete.Partial == partial.Partial {
		t.Error("a partial summary is indistinguishable from a complete one")
	}
	if partial.MissingBatches == 0 {
		t.Error("a partial summary does not report how many batches were lost")
	}
}

// The flag is omitempty so a complete summary's JSON is unchanged for existing
// consumers, while a partial one is explicitly marked.
func TestTransportSummaryJSONOnlyMarksPartial(t *testing.T) {
	if got := marshalSummary(t, TransportSummary{Total: 5}); contains(got, "partial") {
		t.Errorf("a complete summary should not emit a partial field: %s", got)
	}
	if got := marshalSummary(t, TransportSummary{Total: 5, Partial: true, MissingBatches: 2}); !contains(got, "partial") {
		t.Errorf("a partial summary must emit the partial field: %s", got)
	}
}

func marshalSummary(t *testing.T, s TransportSummary) string {
	t.Helper()
	b, err := jsonMarshal(s)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func contains(hay, needle string) bool {
	for i := 0; i+len(needle) <= len(hay); i++ {
		if hay[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}

func jsonMarshal(v any) ([]byte, error) { return json.Marshal(v) }
