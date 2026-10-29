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
	var running, peak, done int32
	hvForEach(pks, func(int, cipher.PubKey) {
		n := atomic.AddInt32(&running, 1)
		for {
			p := atomic.LoadInt32(&peak)
			if n <= p || atomic.CompareAndSwapInt32(&peak, p, n) {
				break
			}
		}
		time.Sleep(5 * time.Millisecond)
		atomic.AddInt32(&running, -1)
		atomic.AddInt32(&done, 1)
	})
	require.Equal(t, int32(10), done)
	require.LessOrEqual(t, peak, int32(hvAppsLimit))
}
