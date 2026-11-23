package visor

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/services"
	"github.com/skycoin/skywire/pkg/visor/visorconfig"
)

// neighborReader keeps the Neighbors its host gives it.
type neighborReader struct {
	mu        sync.Mutex
	neighbors services.Neighbors
}

func (r *neighborReader) Run(ctx context.Context) error { <-ctx.Done(); return nil }

func (r *neighborReader) NeighborsSet(n services.Neighbors) {
	r.mu.Lock()
	r.neighbors = n
	r.mu.Unlock()
}

func (r *neighborReader) neighbor(svcType string) services.Service {
	r.mu.Lock()
	n := r.neighbors
	r.mu.Unlock()
	if n == nil {
		return nil
	}
	return n.NeighborByType(svcType)
}

type neighborSource struct{}

func (*neighborSource) Run(ctx context.Context) error { <-ctx.Done(); return nil }

var (
	neighborTypesOnce sync.Once
	neighborReaders   = make(chan *neighborReader, 8)
)

func neighborTypesRegister() {
	neighborTypesOnce.Do(func() {
		services.Register("test-neighbor-reader", func(json.RawMessage, *logging.Logger) (services.Service, error) {
			r := &neighborReader{}
			neighborReaders <- r
			return r, nil
		})
		services.Register("test-neighbor-source", func(json.RawMessage, *logging.Logger) (services.Service, error) {
			return &neighborSource{}, nil
		})
	})
}

// An embedded service finds another running in the same visor, and finds
// nothing once that one is stopped.
func TestEmbeddedServiceFindsNeighbor(t *testing.T) {
	neighborTypesRegister()
	block := func(typ string) services.Block {
		_, sk := cipher.GenerateKeyPair()
		raw, err := json.Marshal(map[string]string{"type": typ, "secret_key": sk.Hex()})
		require.NoError(t, err)
		var b services.Block
		require.NoError(t, json.Unmarshal(raw, &b))
		return b
	}
	visorPK, visorSK := cipher.GenerateKeyPair()
	common := &visorconfig.Common{PK: visorPK, SK: visorSK}
	common.SetLogger(logging.NewMasterLogger())
	v := &Visor{conf: &visorconfig.V1{Common: common, EmbeddedServices: []services.Block{
		block("test-neighbor-reader"), block("test-neighbor-source"),
	}}}
	require.NoError(t, initOwnKeyServices(t.Context(), v, logging.MustGetLogger("test")))

	var reader *neighborReader
	select {
	case reader = <-neighborReaders:
	case <-time.After(5 * time.Second):
		t.Fatal("the reader never started")
	}
	require.Eventually(t, func() bool {
		_, ok := reader.neighbor("test-neighbor-source").(*neighborSource)
		return ok
	}, 5*time.Second, 10*time.Millisecond)
	require.Nil(t, reader.neighbor("transport-discovery"))

	require.NoError(t, v.EmbeddedServiceControl("test-neighbor-source", "stop"))
	require.Eventually(t, func() bool { return reader.neighbor("test-neighbor-source") == nil }, 5*time.Second, 10*time.Millisecond)
}
