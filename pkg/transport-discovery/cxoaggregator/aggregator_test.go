package cxoaggregator

import (
	"testing"

	"github.com/google/uuid"
)

func TestParseCurrentTransportPath(t *testing.T) {
	id := uuid.New()
	cases := []struct {
		path string
		want bool
	}{
		{"transports/" + id.String() + "/current", true},
		{"transports/" + id.String() + "/2026-04-27", false}, // daily rollup, not "current"
		{"transports//current", false},                       // empty UUID segment
		{"tiers/dmsg/current", false},                        // wrong prefix
		{"transports/not-a-uuid/current", false},
		{"transports/" + id.String() + "/current/extra", false}, // extra segment
		{"", false},
	}
	for _, c := range cases {
		gotID, ok := parseCurrentTransportPath(c.path)
		if ok != c.want {
			t.Errorf("parseCurrentTransportPath(%q) ok = %v, want %v", c.path, ok, c.want)
			continue
		}
		if c.want && gotID != id {
			t.Errorf("parseCurrentTransportPath(%q) id = %v, want %v", c.path, gotID, id)
		}
	}
}
