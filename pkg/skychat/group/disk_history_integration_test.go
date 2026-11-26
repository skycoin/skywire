// Package group pkg/skychat/group/disk_history_integration_test.go c3-app-chat
//
// A group's history lives in the on-disk CXO store, and only a bounded
// window of it in memory. A member that joins late gets the whole history
// streamed from the owner's store, and an owner restart reads none of it
// back into memory.
//
// GROUP_HISTORY_N raises the message count for a memory measurement; the
// test then logs live heap at each step.
package group

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/logging"
)

func liveHeapMB() float64 {
	runtime.GC()
	runtime.GC()
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	return float64(ms.HeapAlloc) / 1e6
}

// heapPeakSample samples the heap every 10 ms until the returned func stops it
// and reports the largest value seen.
func heapPeakSample() func() float64 {
	done := make(chan struct{})
	peak := make(chan float64, 1)
	go func() {
		var ms runtime.MemStats
		maxMB := 0.0
		t := time.NewTicker(10 * time.Millisecond)
		defer t.Stop()
		for {
			runtime.ReadMemStats(&ms)
			maxMB = max(maxMB, float64(ms.HeapAlloc)/1e6)
			select {
			case <-done:
				peak <- maxMB
				return
			case <-t.C:
			}
		}
	}()
	return func() float64 {
		close(done)
		return <-peak
	}
}

func TestDiskHistory_LateMemberGetsHistoryAndRestartStaysSmall(t *testing.T) {
	n := 300
	if v, err := strconv.Atoi(os.Getenv("GROUP_HISTORY_N")); err == nil && v > 0 {
		n = v
	}
	a, b := newGroupEnv(t)
	baseHeap := liveHeapMB()

	rec, err := a.mgr.Create("history room", KindPublic, nil)
	require.NoError(t, err)
	_, err = a.mgr.AddMember(rec.ID, b.pk)
	require.NoError(t, err)

	a.mgr.mu.RLock()
	sess := a.mgr.sessions[rec.ID]
	a.mgr.mu.RUnlock()
	require.NotNil(t, sess)
	start := time.Now()
	ts0 := time.Now().Add(-time.Duration(n) * time.Minute).UTC()
	for i := range n {
		require.NoError(t, sess.publishAs(a.pk, fmt.Sprintf("history %d", i), ts0.Add(time.Duration(i)*time.Minute)))
		if i%200 == 199 {
			require.NoError(t, sess.pub.Flush())
		}
	}
	require.NoError(t, sess.pub.Flush())
	t.Logf("published %d messages in %s; owner heap +%.1f MB", n, time.Since(start).Round(time.Millisecond), liveHeapMB()-baseHeap)

	link, err := a.mgr.BuildInvite(rec.ID)
	require.NoError(t, err)
	inv, err := DecodeInvite(link)
	require.NoError(t, err)
	start = time.Now()
	// Count rather than keep what the member receives, so the test holds
	// no copy of the history itself.
	var got atomic.Int64
	last := fmt.Sprintf("history %d", n-1)
	var gotLast atomic.Bool
	b.mgr.SetMessageHandler(func(_ string, _ cipher.PubKey, m Message) {
		got.Add(1)
		if m.Text == last {
			gotLast.Store(true)
		}
		if m.Text == "after the restart" {
			b.inbox.deliver("", m.SenderPK, m)
		}
	})
	peakFill := heapPeakSample()
	_, err = b.mgr.Join(inv)
	require.NoError(t, err)
	deadline := time.Now().Add(time.Duration(60+n/50) * time.Second)
	for !gotLast.Load() {
		require.True(t, time.Now().Before(deadline), "member got %d of %d messages", got.Load(), n)
		time.Sleep(200 * time.Millisecond)
	}
	t.Logf("member backfilled %d messages in %s; both visors heap +%.1f MB, peak +%.1f MB", got.Load(),
		time.Since(start).Round(time.Millisecond), liveHeapMB()-baseHeap, peakFill()-baseHeap)
	require.GreaterOrEqual(t, got.Load(), int64(n))

	// Restart the owner on the same store.
	require.NoError(t, a.mgr.Close())
	time.Sleep(500 * time.Millisecond)
	closedHeap := liveHeapMB()
	mgr2, err := NewManager(ManagerConfig{
		Store: a.store, DmsgC: a.dmsgC, MyPK: a.pk, MySK: a.sk,
		DataDir: filepath.Join(a.dir, "cxo"),
		Logger:  logging.MustGetLogger("group-mgr-A2"),
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = mgr2.Close() }) //nolint:errcheck
	replay := &msgInbox{}
	mgr2.SetMessageHandler(replay.deliver)
	start = time.Now()
	peak := heapPeakSample()
	require.NoError(t, mgr2.Resume())
	t.Logf("owner resumed in %s; heap +%.1f MB over the closed owner, peak +%.1f MB", time.Since(start).Round(time.Millisecond),
		liveHeapMB()-closedHeap, peak()-closedHeap)
	require.Len(t, replay.snapshot(), resumeReplayMessageCap, "resume replays the newest messages")
	require.Equal(t, last, replay.snapshot()[resumeReplayMessageCap-1].Text)

	ctx, cancel := context.WithTimeout(context.Background(), 240*time.Second)
	defer cancel()
	go func() {
		for ctx.Err() == nil {
			_ = mgr2.SendToGroup(ctx, rec.ID, "after the restart") //nolint:errcheck
			time.Sleep(2 * time.Second)
		}
	}()
	// The member would redial on its quiet watchdog within about a minute.
	b.mgr.mu.RLock()
	bs := b.mgr.sessions[rec.ID]
	b.mgr.mu.RUnlock()
	require.NotNil(t, bs)
	_ = bs.ReconnectLegacySub(ctx)  //nolint:errcheck
	_ = bs.ReconnectPeer(ctx, a.pk) //nolint:errcheck
	start = time.Now()
	for !hasText("after the restart")(b.inbox.snapshot()) {
		require.NoError(t, ctx.Err(), "no message after the owner restart")
		time.Sleep(200 * time.Millisecond)
	}
	t.Logf("member got a message %s after the owner resumed", time.Since(start).Round(time.Millisecond))
}
