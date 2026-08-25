// Package store pkg/deployment/monitor/store/store_test.go: unit tests for the
// network-monitor store constructor and its in-memory implementation.
package store

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cxo/storeconfig"
	nm "github.com/skycoin/skywire/pkg/deployment/monitor/types"
)

// TestNewMemory verifies the constructor returns a working in-memory store.
func TestNewMemory(t *testing.T) {
	s, err := New(storeconfig.Config{Type: storeconfig.Memory})
	require.NoError(t, err)
	require.NotNil(t, s)

	// A fresh store reports a zero-valued status.
	status, err := s.GetNetworkStatus()
	require.NoError(t, err)
	assert.Equal(t, nm.Status{}, status)
}

// TestNewUnknownType verifies an unsupported store type is rejected.
func TestNewUnknownType(t *testing.T) {
	s, err := New(storeconfig.Config{Type: storeconfig.Redis})
	assert.Error(t, err)
	assert.Nil(t, s)
}

// TestMemoryStoreRoundTrip verifies a stored status is read back unchanged.
func TestMemoryStoreRoundTrip(t *testing.T) {
	s, err := New(storeconfig.Config{Type: storeconfig.Memory})
	require.NoError(t, err)

	want := nm.Status{
		LastUpdate:   time.Now().UTC(),
		OnlineVisors: 5,
		Transports:   12,
		VPN:          3,
		Skysocks:     2,
		PublicVisor:  4,
		LastCleaning: &nm.LastCleaningSummary{AllDeadEntriesCleaned: 9, Tpd: 1, Dmsgd: 2},
	}
	require.NoError(t, s.SetNetworkStatus(want))

	got, err := s.GetNetworkStatus()
	require.NoError(t, err)
	assert.Equal(t, want, got)

	// A subsequent write overwrites the previous value.
	require.NoError(t, s.SetNetworkStatus(nm.Status{OnlineVisors: 1}))
	got, err = s.GetNetworkStatus()
	require.NoError(t, err)
	assert.Equal(t, 1, got.OnlineVisors)
	assert.Equal(t, 0, got.Transports)
}
