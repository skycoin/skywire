// Package dmsgclient pkg/dmsg/dmsgclient/fallback_disc_test.go c1-net-dmsg
//
// Locks the fallbackDiscClient server-enumeration contract (the discovery half
// of the wasm-visor 202-rendezvous fix): AllServers must PREFER the live
// discovery — a seeded browser edge otherwise stays pinned to its 2 seed
// servers forever and can never rendezvous with peers delegated elsewhere —
// while AvailableServers stays on the direct (seed) client, because it is the
// boot / session-maintenance path and must never block on a discovery that is
// only reachable once dmsg is up.
package dmsgclient

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/disc"
	"github.com/skycoin/skywire/pkg/logging"
)

// stubDisc implements only the EntryReader surface the tests exercise; the
// embedded interface makes any un-stubbed call an explicit panic.
type stubDisc struct {
	disc.APIClient
	all       []*disc.Entry
	allErr    error
	available []*disc.Entry
}

func (s *stubDisc) AllServers(context.Context) ([]*disc.Entry, error) { return s.all, s.allErr }
func (s *stubDisc) AvailableServers(context.Context) ([]*disc.Entry, error) {
	return s.available, nil
}

func serverEntries(n int) []*disc.Entry {
	out := make([]*disc.Entry, n)
	for i := range out {
		pk, _ := cipher.GenerateKeyPair()
		out[i] = &disc.Entry{Static: pk, Server: &disc.Server{Address: "127.0.0.1:1"}}
	}
	return out
}

func TestFallbackDisc_AllServersPrefersLiveDiscovery(t *testing.T) {
	seeds := serverEntries(2)
	live := serverEntries(9)
	f := NewRegisteringFallbackDiscClient(
		&stubDisc{all: seeds, available: seeds},
		&stubDisc{all: live},
		logging.MustGetLogger("test"),
	)

	got, err := f.AllServers(context.Background())
	require.NoError(t, err)
	require.Len(t, got, 9, "AllServers must return the LIVE deployment set, not the 2 seeds")
}

func TestFallbackDisc_AllServersFallsBackToSeedsOnLiveFailure(t *testing.T) {
	seeds := serverEntries(2)
	f := NewRegisteringFallbackDiscClient(
		&stubDisc{all: seeds, available: seeds},
		&stubDisc{allErr: errors.New("discovery unreachable")},
		logging.MustGetLogger("test"),
	)

	got, err := f.AllServers(context.Background())
	require.NoError(t, err)
	require.Len(t, got, 2, "on live-discovery failure the seed set keeps the client functional")
}

func TestFallbackDisc_AllServersFallsBackToSeedsOnEmptyLive(t *testing.T) {
	seeds := serverEntries(2)
	f := NewRegisteringFallbackDiscClient(
		&stubDisc{all: seeds, available: seeds},
		&stubDisc{all: nil},
		logging.MustGetLogger("test"),
	)

	got, err := f.AllServers(context.Background())
	require.NoError(t, err)
	require.Len(t, got, 2)
}

func TestFallbackDisc_AvailableServersStaysDirect(t *testing.T) {
	seeds := serverEntries(2)
	live := serverEntries(9)
	f := NewRegisteringFallbackDiscClient(
		&stubDisc{all: seeds, available: seeds},
		&stubDisc{all: live, available: live},
		logging.MustGetLogger("test"),
	)

	got, err := f.AvailableServers(context.Background())
	require.NoError(t, err)
	require.Len(t, got, 2, "AvailableServers is the boot path — it must answer from the direct/seed client without touching the live discovery")
}
