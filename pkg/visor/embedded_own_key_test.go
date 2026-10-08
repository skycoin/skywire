package visor

import (
	"context"
	"encoding/json"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/services"
	"github.com/skycoin/skywire/pkg/visor/visorconfig"
)

var ownKeyRuns atomic.Int32

type flakyOwnKeyService struct{}

// Run fails on its first start and then runs until ctx ends.
func (flakyOwnKeyService) Run(ctx context.Context) error {
	if ownKeyRuns.Add(1) == 1 {
		return errors.New("first start fails")
	}
	<-ctx.Done()
	return nil
}

func init() {
	services.Register("test-own-key", func(json.RawMessage, *logging.Logger) (services.Service, error) {
		return flakyOwnKeyService{}, nil
	})
}

// A block with a key of its own runs as its own service, not mounted, under
// that key, and is started again after it stops.
func TestEmbeddedServiceRunsUnderItsOwnKey(t *testing.T) {
	ownKeyMinBackoff = 10 * time.Millisecond
	t.Cleanup(func() { ownKeyMinBackoff = 5 * time.Second })

	visorPK, visorSK := cipher.GenerateKeyPair()
	svcPK, svcSK := cipher.GenerateKeyPair()
	raw, err := json.Marshal(map[string]string{"type": "test-own-key", "secret_key": svcSK.Hex()})
	require.NoError(t, err)
	var b services.Block
	require.NoError(t, json.Unmarshal(raw, &b))

	common := &visorconfig.Common{PK: visorPK, SK: visorSK}
	common.SetLogger(logging.NewMasterLogger())
	v := &Visor{conf: &visorconfig.V1{Common: common, EmbeddedServices: []services.Block{b}}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	require.NoError(t, initEmbeddedServices(ctx, v, logging.MustGetLogger("test")), "no dmsg HTTP mux is needed")

	require.Eventually(t, func() bool {
		st := v.embeddedServiceStates()
		return len(st) == 1 && st[0].Running && st[0].Restarts == 1
	}, 5*time.Second, 10*time.Millisecond)
	st := v.embeddedServiceStates()[0]
	require.True(t, st.OwnKey)
	require.Equal(t, "dmsg://"+svcPK.Hex(), st.URL)
}
