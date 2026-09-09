package cliconfig

import (
	"reflect"
	"testing"
)

// TestFlagArgsForSkyenv pins the SKYENV-variable → gen-flag direction the
// in-process autoconfig re-entry depends on: each kind of variable maps to
// the flag form gen accepts for it, and an unknown variable maps to nothing.
func TestFlagArgsForSkyenv(t *testing.T) {
	for _, tc := range []struct {
		key, value string
		want       []string
		ok         bool
	}{
		{"DISABLEPUBLICAUTOCONN", "true", []string{"--autoconn=true"}, true},
		{"DISABLEPUBLICAUTOCONN", "false", []string{"--autoconn=false"}, true},
		{"WSPEERS", "pk@ws://node.local:8000/tp/ws", []string{"--ws-peer", "pk@ws://node.local:8000/tp/ws"}, true},
		{"HYPERVISORPKS", "a,b", []string{"--hvpks", "a,b"}, true},
		{"TRANSPORTPORT", "7777", []string{"--transport-port", "7777"}, true},
		{"REWARDSKYADDR", "2jBbGxZ", []string{"--reward", "2jBbGxZ"}, true},
		{"NOSUCHKEY", "x", nil, false},
	} {
		got, ok := FlagArgsForSkyenv(tc.key, tc.value)
		if ok != tc.ok || !reflect.DeepEqual(got, tc.want) {
			t.Errorf("FlagArgsForSkyenv(%s, %q) = %v, %v; want %v, %v", tc.key, tc.value, got, ok, tc.want, tc.ok)
		}
	}
}
