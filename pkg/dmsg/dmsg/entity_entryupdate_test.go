package dmsg

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/logging"
)

// TestEntryUpdateSemSerializes covers the mutual exclusion that keeps two
// concurrent publishes from interleaving their GET/PUT and having one rejected
// 422 "not old + 1" (#4086).
func TestEntryUpdateSemSerializes(t *testing.T) {
	sem := make(chan struct{}, 1)
	sem <- struct{}{} // held by a publisher in flight

	select {
	case sem <- struct{}{}:
		t.Fatal("a second publisher acquired the semaphore while one was in flight")
	default:
	}

	<-sem // holder finishes
	select {
	case sem <- struct{}{}:
	default:
		t.Fatal("semaphore not released")
	}
}

// TestEntryUpdateSemHonoursContext is the property that distinguishes this from
// a sync.Mutex. #3157/#3168 wedged services precisely because mutex acquisition
// ignores context: one stuck PUT held the lock and every other caller blocked
// forever, their own per-attempt timeouts useless. A caller here must give up
// when its context expires and retry on the next tick instead.
func TestEntryUpdateSemHonoursContext(t *testing.T) {
	sem := make(chan struct{}, 1)
	sem <- struct{}{} // a stuck publisher never releases

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	start := time.Now()
	var err error
	select {
	case sem <- struct{}{}:
		t.Fatal("acquired a held semaphore")
	case <-ctx.Done():
		err = ctx.Err()
	}
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.Less(t, time.Since(start), time.Second, "caller should give up with its context, not block")
}

// TestEntryUpdateSemInitialised guards the nil-channel case: a send on a nil
// channel blocks forever, so init must create the semaphore and the acquire
// site must tolerate a zero-value EntityCommon.
func TestEntryUpdateSemInitialised(t *testing.T) {
	var c EntityCommon
	require.Nil(t, c.entryUpdateSem, "zero value has no semaphore; acquire site must nil-check")

	pk, sk := cipher.GenerateKeyPair()
	c.init(pk, sk, nil, logging.MustGetLogger("test"), 0)
	require.NotNil(t, c.entryUpdateSem)
	require.Equal(t, 1, cap(c.entryUpdateSem))
}
