package dmsgclient

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/direct"
	"github.com/skycoin/skywire/pkg/dmsg/disc"
	"github.com/skycoin/skywire/pkg/logging"
)

// A folded visor reads its own entry back through this client before adding
// its client half. When the live lookup fails, that edit must not land on the
// unversioned seed, or every later read skips the discovery and the publish is
// refused with "entry has no version" for the life of the process.
func TestFallbackDisc_SelfSeedSurvivesEditAfterLiveFailure(t *testing.T) {
	pk, _ := cipher.GenerateKeyPair()
	log := logging.MustGetLogger("test")
	seeds := direct.NewClient([]*disc.Entry{{Static: pk, Server: &disc.Server{Address: "127.0.0.1:1"}}}, log)
	live := &stubDisc{entryErr: errors.New("cannot connect to delegated server")}
	f := NewRegisteringFallbackDiscClient(seeds, live, log)

	got, err := f.Entry(context.Background(), pk)
	require.NoError(t, err)
	require.Nil(t, got.Client)
	got.Client = &disc.Client{DelegatedServers: []cipher.PubKey{pk}}
	got.Sequence = 50

	again, err := f.Entry(context.Background(), pk)
	require.NoError(t, err)
	require.Nil(t, again.Client, "the caller's edit leaked into the seed")
	require.Zero(t, again.Sequence)
	require.Equal(t, 2, live.entryHits, "the live discovery must be asked again")
}

// A config saved while the bug above was live keeps a self seed carrying both
// halves and no version. A seed with a server half must still ask the live
// discovery, or that saved seed shadows the discovery after every restart.
func TestFallbackDisc_DualSeedPrefersLiveEntry(t *testing.T) {
	pk, _ := cipher.GenerateKeyPair()
	seed := &disc.Entry{
		Static: pk,
		Server: &disc.Server{Address: "127.0.0.1:1"},
		Client: &disc.Client{DelegatedServers: []cipher.PubKey{pk}},
	}
	liveEntry := &disc.Entry{Version: "v1", Static: pk, Server: &disc.Server{Address: "127.0.0.1:1"}}
	live := &stubDisc{entry: liveEntry}
	f := NewRegisteringFallbackDiscClient(&stubDisc{entry: seed}, live, logging.MustGetLogger("test"))

	got, err := f.Entry(context.Background(), pk)
	require.NoError(t, err)
	require.Same(t, liveEntry, got)
	require.Equal(t, 1, live.entryHits)
}
