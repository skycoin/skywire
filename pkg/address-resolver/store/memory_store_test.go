package store

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/skycoin/skywire/pkg/cxo/storeconfig"
	"github.com/skycoin/skywire/pkg/logging"
)

func TestMemory(t *testing.T) {
	storeConfig := storeconfig.Config{Type: storeconfig.Memory}

	log := logging.MustGetLogger("test")
	ctx := context.TODO()
	s, err := New(ctx, storeConfig, 10*time.Minute, log)
	require.NoError(t, err)

	suite.Run(t, &AddressSuite{AddressStore: s})
}
