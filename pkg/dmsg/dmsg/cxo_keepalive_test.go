// Package dmsg pkg/dmsg/dmsg/cxo_keepalive_test.go: while a registration-over-
// CXO feed is healthy on the epoch of the last update, the periodic client
// re-registration is never due; a new epoch or an unhealthy feed makes it due.
package dmsg

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/logging"
)

func TestUpdateIsDueFollowsCXOKeepalive(t *testing.T) {
	pk, sk := cipher.GenerateKeyPair()
	c := new(EntityCommon)
	c.init(pk, sk, nil, logging.MustGetLogger("test"), time.Minute)

	var healthy atomic.Bool
	var epoch atomic.Uint64
	c.SetCXOKeepaliveHealthyFunc(func() (bool, uint64) { return healthy.Load(), epoch.Load() })

	healthy.Store(true)
	c.recordUpdate()
	c.lastUpdate.Store(time.Now().Add(-24 * time.Hour).UnixNano())
	_, due := c.updateIsDue()
	require.False(t, due, "healthy on the same epoch: never due")

	epoch.Add(1)
	_, due = c.updateIsDue()
	require.True(t, due, "new epoch: due")

	c.recordUpdate()
	_, due = c.updateIsDue()
	require.False(t, due, "re-registered on the new epoch")

	healthy.Store(false)
	c.lastUpdate.Store(time.Now().Add(-2 * time.Minute).UnixNano())
	_, due = c.updateIsDue()
	require.True(t, due, "unhealthy: the base interval applies")
}
