// Package visor pkg/visor/api_tpd_metrics_subscriber_test.go c3-vis-core
package visor

import (
	"encoding/json"
	"testing"
)

// The reader stitches the publisher's part leaves back into the single array
// its callers expect. Splicing at the byte level is what makes a 30-megabyte
// window affordable, so the boundaries have to be exactly right.
func TestSpliceJSONArrays(t *testing.T) {
	for _, tc := range []struct {
		name  string
		parts []string
		want  string
	}{
		{"single part", []string{`[{"id":"a"}]`}, `[{"id":"a"}]`},
		{"two parts", []string{`[{"id":"a"}]`, `[{"id":"b"},{"id":"c"}]`}, `[{"id":"a"},{"id":"b"},{"id":"c"}]`},
		{"empty part in the middle", []string{`[{"id":"a"}]`, `[]`, `[{"id":"b"}]`}, `[{"id":"a"},{"id":"b"}]`},
		{"all parts empty", []string{`[]`, `[]`}, `[]`},
		{"surrounding whitespace", []string{"  [ {\"id\":\"a\"} ] \n", `[{"id":"b"}]`}, `[{"id":"a"},{"id":"b"}]`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bodies := make([][]byte, len(tc.parts))
			for i, p := range tc.parts {
				bodies[i] = []byte(p)
			}
			got, err := spliceJSONArrays(bodies)
			if err != nil {
				t.Fatalf("spliceJSONArrays: %v", err)
			}
			if string(got) != tc.want {
				t.Errorf("got %s, want %s", got, tc.want)
			}
			var decoded []map[string]any
			if err := json.Unmarshal(got, &decoded); err != nil {
				t.Errorf("result is not valid JSON: %v", err)
			}
		})
	}
}

// A body that is not an array must be reported, not spliced: pasting it between
// the brackets would yield malformed JSON that only fails much further
// downstream, in the hvui.
func TestSpliceJSONArraysRejectsNonArray(t *testing.T) {
	for _, bad := range []string{`{"id":"a"}`, ``, `[`, `null`, `"a"`} {
		if _, err := spliceJSONArrays([][]byte{[]byte(`[{"id":"a"}]`), []byte(bad)}); err == nil {
			t.Errorf("spliceJSONArrays accepted a non-array part %q", bad)
		}
	}
}
