// Package store pkg/deployment/ar/store/address_test.go
package store

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/transport/network/addrresolver"
	types "github.com/skycoin/skywire/pkg/transport/types"
)

type AddressSuite struct {
	suite.Suite
	AddressStore
}

func (s *AddressSuite) SetupTest() {
}

func (s *AddressSuite) TestRegister() {
	t := s.T()
	ctx := context.Background()

	pk, _ := cipher.GenerateKeyPair()

	visorData := addrresolver.VisorData{
		RemoteAddr: "[::1]:1234",
	}

	t.Run(".BindSTCPR", func(t *testing.T) {
		require.NoError(t, s.Bind(ctx, types.STCPR, pk, visorData))
	})

	t.Run(".ResolveSTCPR", func(t *testing.T) {
		got, err := s.Resolve(ctx, types.STCPR, pk)
		require.NoError(t, err)
		require.Equal(t, visorData, got)
	})

	// QUIC must bind + resolve like STCPR. The redis store originally switched
	// only on STCPR/SUDPH and returned ErrUnknownTransportType for QUIC, so the
	// AR 500'd every /resolve/quic and visors could never register QUIC (0 QUIC
	// transports fleet-wide). Guard the round-trip for both store backends.
	t.Run(".BindQUIC", func(t *testing.T) {
		require.NoError(t, s.Bind(ctx, types.QUIC, pk, visorData))
	})

	t.Run(".ResolveQUIC", func(t *testing.T) {
		got, err := s.Resolve(ctx, types.QUIC, pk)
		require.NoError(t, err)
		require.Equal(t, visorData, got)
	})

	// WT carries a self-signed cert hash (no CA) that the dialing peer pins. It
	// rides VisorData via the embedded LocalAddresses, so a bind→resolve must
	// preserve it. Guards both store backends + the WT type in the switch.
	wtData := addrresolver.VisorData{
		RemoteAddr:     "[::1]:5678",
		LocalAddresses: addrresolver.LocalAddresses{CertHash: "deadbeefcafe"},
	}
	t.Run(".BindWT", func(t *testing.T) {
		require.NoError(t, s.Bind(ctx, types.WT, pk, wtData))
	})

	t.Run(".ResolveWT", func(t *testing.T) {
		got, err := s.Resolve(ctx, types.WT, pk)
		require.NoError(t, err)
		require.Equal(t, wtData, got)
		require.Equal(t, "deadbeefcafe", got.CertHash)
	})
}
