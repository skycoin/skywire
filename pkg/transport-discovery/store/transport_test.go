//go:build !no_ci
// +build !no_ci

package store

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/skycoin/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire/pkg/transport"
)

type TransportSuite struct {
	suite.Suite
	TransportStore
}

func (s *TransportSuite) SetupTest() {
}

// nolint:funlen
func (s *TransportSuite) TestRegister() {
	t := s.T()
	ctx := context.Background()

	pk1, _ := cipher.GenerateKeyPair()
	pk2, _ := cipher.GenerateKeyPair()

	sEntry := &transport.SignedEntry{
		Entry: &transport.Entry{
			ID:    uuid.New(),
			Edges: transport.SortEdges(pk1, pk2), // original: [2]cipher.PubKey{pk1, pk2}
			Type:  "dmsg",
		},
		Signatures: [2]cipher.Sig{},
	}

	t.Run(".RegisterTransport", func(t *testing.T) {
		require.NoError(t, s.RegisterTransport(ctx, sEntry))
		assert.True(t, sEntry.Registered > 0)
	})

	t.Run(".GetTransportByID", func(t *testing.T) {
		found, err := s.GetTransportByID(ctx, sEntry.Entry.ID)
		require.NoError(t, err)
		assert.Equal(t, sEntry.Entry, found)
	})

	t.Run(".GetTransportsByEdge", func(t *testing.T) {
		entries, err := s.GetTransportsByEdge(ctx, pk1)
		require.NoError(t, err)
		require.Len(t, entries, 1)
		assert.Equal(t, sEntry.Entry, entries[0])

		entries, err = s.GetTransportsByEdge(ctx, pk2)
		require.NoError(t, err)
		require.Len(t, entries, 1)
		assert.Equal(t, sEntry.Entry, entries[0])

		pk, _ := cipher.GenerateKeyPair()
		_, err = s.GetTransportsByEdge(ctx, pk)
		require.Error(t, err)
	})

	t.Run(".DeregisterTransport", func(t *testing.T) {
		err := s.DeregisterTransport(ctx, sEntry.Entry.ID)
		require.NoError(t, err)

		_, err = s.GetTransportByID(ctx, sEntry.Entry.ID)
		require.Error(t, err)
		assert.Equal(t, "transport not found", err.Error())
	})
}
