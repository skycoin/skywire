package clivisor

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
)

func TestHVForEachLimitsConcurrency(t *testing.T) {
	pks := make([]cipher.PubKey, 10)
	var running, peak, done atomic.Int32
	hvForEach(pks, func(int, cipher.PubKey) {
		n := running.Add(1)
		for {
			p := peak.Load()
			if n <= p || peak.CompareAndSwap(p, n) {
				break
			}
		}
		time.Sleep(5 * time.Millisecond)
		running.Add(-1)
		done.Add(1)
	})
	require.Equal(t, int32(10), done.Load())
	require.LessOrEqual(t, peak.Load(), int32(hvAppsLimit))
}
