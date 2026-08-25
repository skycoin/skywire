package store

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/transport/network/addrresolver"
	types "github.com/skycoin/skywire/pkg/transport/types"
)

// TestResolveLegacyTypeAlias guards the AR /resolve 500 regression: the WT/QUIC
// renames made types.WT/types.QUIC "swtr"/"squicr", but clients (and the
// resolve URL) still send the legacy "wt"/"quic". Without normalization the
// store's type switch missed every case → ErrUnknownTransportType → HTTP 500.
// A bind under the canonical type must be resolvable by the legacy name (and
// vice-versa), and an unknown type must still be ErrUnknownTransportType.
func TestResolveLegacyTypeAlias(t *testing.T) {
	ctx := context.Background()
	s := newMemoryStore()
	pk, _ := cipher.GenerateKeyPair()
	data := addrresolver.VisorData{RemoteAddr: "1.2.3.4:5678"}

	// Bind via the canonical constants (what the bind handlers pass).
	require.NoError(t, s.Bind(ctx, types.WT, pk, data))
	require.NoError(t, s.Bind(ctx, types.QUIC, pk, data))

	for _, name := range []types.Type{"wt", "swtr", "quic", "squic", "squicr"} {
		got, err := s.Resolve(ctx, name, pk)
		require.NoErrorf(t, err, "resolve %q must not error (was the AR 500)", name)
		require.Equal(t, data.RemoteAddr, got.RemoteAddr, "resolve %q", name)
	}

	// A genuinely unknown type cleanly misses (the mem store reports ErrNoEntry →
	// HTTP 404), never a hit and never the 500-causing path.
	_, err := s.Resolve(ctx, "bogus", pk)
	require.ErrorIs(t, err, ErrNoEntry)

	// Bind via a legacy name resolves under the canonical name too.
	pk2, _ := cipher.GenerateKeyPair()
	require.NoError(t, s.Bind(ctx, "wt", pk2, data))
	got, err := s.Resolve(ctx, types.WT, pk2)
	require.NoError(t, err)
	require.Equal(t, data.RemoteAddr, got.RemoteAddr)
}
