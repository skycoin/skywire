//go:build !no_ci
// +build !no_ci

// Package store pkg/address-resolver/store/address_test.go
package store

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/skycoin/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire/pkg/transport/network"
	"github.com/skycoin/skywire/pkg/transport/network/addrresolver"
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
		require.NoError(t, s.Bind(ctx, network.STCPR, pk, visorData))
	})

	t.Run(".ResolveSTCPR", func(t *testing.T) {
		got, err := s.Resolve(ctx, network.STCPR, pk)
		require.NoError(t, err)
		require.Equal(t, visorData, got)
	})
}
