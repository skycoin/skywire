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
		{"LOGLVL", "debug", []string{"--loglvl", "debug"}, true},
		{"NOSUCHKEY", "x", nil, false},
	} {
		got, ok := FlagArgsForSkyenv(tc.key, tc.value)
		if ok != tc.ok || !reflect.DeepEqual(got, tc.want) {
			t.Errorf("FlagArgsForSkyenv(%s, %q) = %v, %v; want %v, %v", tc.key, tc.value, got, ok, tc.want, tc.ok)
		}
	}
}

// TestFlagArgsForSkyenvResolvers. The resolving-proxy knobs were absent from
// the flag↔SKYENV maps, so they could not round-trip: `config gen --dmsgweb`
// emitted a conf with DMSGWEB still commented, and generating from that conf
// produced a config with no dmsg_web block. The proxy chain came back up
// silently disabled — nothing listening on 4445 or 4446 — which is invisible
// until someone tries to use it.
func TestFlagArgsForSkyenvResolvers(t *testing.T) {
	for _, tc := range []struct {
		key, value string
		want       []string
	}{
		{"DMSGWEB", "true", []string{"--dmsgweb=true"}},
		{"SKYNETWEB", "true", []string{"--skynetweb=true"}},
		{"DMSGWEBUPSTREAM", "127.0.0.1:4446", []string{"--dmsgweb-upstream", "127.0.0.1:4446"}},
		{"SKYNETWEBUPSTREAM", "127.0.0.1:1080", []string{"--skynetweb-upstream", "127.0.0.1:1080"}},
		{"DMSGWEBADDR", "0.0.0.0", []string{"--dmsgweb-addr", "0.0.0.0"}},
		{"SKYNETWEBADDR", "0.0.0.0", []string{"--skynetweb-addr", "0.0.0.0"}},
	} {
		got, ok := FlagArgsForSkyenv(tc.key, tc.value)
		if !ok || !reflect.DeepEqual(got, tc.want) {
			t.Errorf("FlagArgsForSkyenv(%s, %q) = %v, %v; want %v, true — "+
				"the chain cannot survive a conf round-trip without it",
				tc.key, tc.value, got, ok, tc.want)
		}
	}
}

// TestEveryResolverFlagRoundTrips is the guard against the NEXT one going
// missing: every gen flag whose name starts with dmsgweb/skynetweb has to be
// reachable from some SKYENV variable, or it silently stops surviving a
// regenerate.
func TestEveryResolverFlagRoundTrips(t *testing.T) {
	mapped := map[string]bool{}
	for flag := range flagToEnv {
		mapped[flag] = true
	}
	for _, m := range []map[string]string{valueFlagToEnv, intFlagToEnv, arrayFlagToEnv} {
		for flag := range m {
			mapped[flag] = true
		}
	}
	for _, flag := range []string{
		"dmsgweb", "dmsgweb-upstream", "dmsgweb-addr", "dmsgweb-sk",
		"skynetweb", "skynetweb-upstream", "skynetweb-addr",
	} {
		if !mapped[flag] {
			t.Errorf("--%s is in no flag↔SKYENV map, so it cannot round-trip through skywire.conf", flag)
		}
	}
}
