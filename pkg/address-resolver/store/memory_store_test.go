//go:build !no_ci
// +build !no_ci

package store

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/skycoin/skywire-utilities/pkg/logging"
	"github.com/skycoin/skywire-utilities/pkg/storeconfig"
)

func TestMemory(t *testing.T) {
	storeConfig := storeconfig.Config{Type: storeconfig.Memory}

	log := logging.MustGetLogger("test")
	ctx := context.TODO()
	s, err := New(ctx, storeConfig, log)
	require.NoError(t, err)

	suite.Run(t, &AddressSuite{AddressStore: s})
}
