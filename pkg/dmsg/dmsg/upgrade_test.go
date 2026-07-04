package dmsg

import (
	"testing"
)

func TestPrefersWTOverWS(t *testing.T) {
	cases := []struct {
		name     string
		carriers []string
		want     bool
	}{
		{"browser wt-first", []string{CarrierWT, CarrierWS}, true},
		{"ws-first", []string{CarrierWS, CarrierWT}, false},
		{"ws-only", []string{CarrierWS}, false},
		{"wt-only", []string{CarrierWT}, false},
		{"native default (empty)", nil, false},
		{"native tcp", []string{CarrierTCP}, false},
		{"wt before ws with tcp tail", []string{CarrierWT, CarrierWS, CarrierTCP}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := prefersWTOverWS(c.carriers); got != c.want {
				t.Fatalf("prefersWTOverWS(%v) = %v, want %v", c.carriers, got, c.want)
			}
		})
	}
}
