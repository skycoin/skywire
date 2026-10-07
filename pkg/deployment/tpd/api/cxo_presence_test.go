package api

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/cxo/storeconfig"
	tpdiscmetrics "github.com/skycoin/skywire/pkg/deployment/tpd/metrics"
	"github.com/skycoin/skywire/pkg/deployment/tpd/store"
	"github.com/skycoin/skywire/pkg/httpauth"
)

type heartbeatSpy struct {
	store.Store
	beats map[cipher.PubKey]int
}

func (s *heartbeatSpy) RecordHeartbeat(_ context.Context, pk cipher.PubKey, _ string) error {
	s.beats[pk]++
	return nil
}

// A visor with no transports publishes an empty list; reconciling it must
// still record the visor as present, or its uptime would depend on HTTP.
func TestReconcileRecordsPresenceWithoutTransports(t *testing.T) {
	ctx := context.Background()
	nonces, err := httpauth.NewNonceStore(ctx, storeconfig.Config{Type: storeconfig.Memory}, "")
	require.NoError(t, err)
	spy := &heartbeatSpy{Store: newTestStore(t).(store.Store), beats: map[cipher.PubKey]int{}}
	api := New(nil, spy, nonces, false, tpdiscmetrics.NewEmpty(), "", "")

	pk, _ := cipher.GenerateKeyPair()
	require.NoError(t, api.ReconcileTransportsFromCXO(ctx, nil, pk, "v1.3.99"))
	require.Equal(t, 1, spy.beats[pk])
	require.NoError(t, api.RefreshTransportsFromCXO(ctx, nil, pk, "v1.3.99"))
	require.Equal(t, 2, spy.beats[pk])
}
