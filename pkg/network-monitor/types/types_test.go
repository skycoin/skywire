// Package nm pkg/network-monitor/types/types_test.go
//
// Status and LastCleaningSummary are pure schema types with no executable
// statements, so there is no statement coverage to gain here. These tests guard
// their JSON wire format: Status is the body served by the network-monitor
// /status HTTP endpoint, so its key names are a contract for external consumers.
// The tests fail loudly if a tag is renamed or the nested shape changes.
package nm

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fullStatus returns a Status with every field set to a distinct value.
func fullStatus() Status {
	s := Status{
		LastUpdate:   time.Date(2026, 6, 20, 12, 0, 0, 0, time.UTC),
		OnlineVisors: 1,
		Transports:   2,
		VPN:          3,
		Skysocks:     4,
		PublicVisor:  5,
		LastCleaning: &LastCleaningSummary{
			AllDeadEntriesCleaned: 6,
			Tpd:                   7,
			Dmsgd:                 8,
			VPN:                   9,
			Skysocks:              10,
			PublicVisor:           11,
		},
	}
	s.LastCleaning.Ar.SUDPH = 12
	s.LastCleaning.Ar.STCPR = 13
	return s
}

// TestStatusRoundTrip verifies a fully-populated Status survives a
// marshal/unmarshal cycle unchanged, including the nested cleaning summary.
func TestStatusRoundTrip(t *testing.T) {
	want := fullStatus()

	raw, err := json.Marshal(want)
	require.NoError(t, err)

	var got Status
	require.NoError(t, json.Unmarshal(raw, &got))
	assert.Equal(t, want, got)
}

// TestStatusWireFormat pins the JSON key names of the /status response,
// including the nested last_cleaning and address_resolver objects.
func TestStatusWireFormat(t *testing.T) {
	raw, err := json.Marshal(fullStatus())
	require.NoError(t, err)

	var top map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(raw, &top))

	for _, key := range []string{
		"last_update", "online_visors", "alive_transports", "available_vpn",
		"available_skysocks", "available_public_visor", "last_cleaning",
	} {
		_, ok := top[key]
		assert.Truef(t, ok, "expected top-level key %q", key)
	}

	var cleaning map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(top["last_cleaning"], &cleaning))
	for _, key := range []string{
		"all_dead_entries_cleaned", "transport_discovery", "address_resolver",
		"dmsg_discovery", "vpn", "skysocks", "public_visor",
	} {
		_, ok := cleaning[key]
		assert.Truef(t, ok, "expected last_cleaning key %q", key)
	}

	var ar struct {
		SUDPH int `json:"sudph"`
		STCPR int `json:"stcpr"`
	}
	require.NoError(t, json.Unmarshal(cleaning["address_resolver"], &ar))
	assert.Equal(t, 12, ar.SUDPH)
	assert.Equal(t, 13, ar.STCPR)
}

// TestStatusNilCleaning verifies the zero value (nil LastCleaning) marshals and
// round-trips without error — the store hands out a zero Status before the
// first cleaning cycle.
func TestStatusNilCleaning(t *testing.T) {
	raw, err := json.Marshal(Status{})
	require.NoError(t, err)

	var got Status
	require.NoError(t, json.Unmarshal(raw, &got))
	assert.Nil(t, got.LastCleaning)
}
