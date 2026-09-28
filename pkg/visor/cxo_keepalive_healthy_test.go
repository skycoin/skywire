// Package visor pkg/visor/cxo_keepalive_healthy_test.go: a recent announce is
// not enough to stretch an HTTP keepalive; the service must be subscribed.
package visor

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/cxo/treestore"
)

func TestCXOKeepaliveHealthyNeedsSubscription(t *testing.T) {
	_, sk := cipher.GenerateKeyPair()
	pub, err := treestore.NewWithTCP("127.0.0.1:0", sk, treestore.PubConfig{InMemoryDB: true})
	require.NoError(t, err)
	t.Cleanup(func() { _ = pub.Close() }) //nolint:errcheck

	svc, _ := cipher.GenerateKeyPair()
	lastOK := new(atomic.Int64)
	healthy := cxoKeepaliveHealthy(pub, svc, lastOK)

	require.False(t, healthy(), "never announced")
	lastOK.Store(time.Now().UnixNano())
	require.False(t, healthy(), "announced but not subscribed")
}
