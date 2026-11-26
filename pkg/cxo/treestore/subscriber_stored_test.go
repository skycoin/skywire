package treestore

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
)

type eventLog struct {
	mu     sync.Mutex
	events map[string]string
	calls  int
}

func (l *eventLog) record(events []UpdateEvent) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.calls++
	for _, ev := range events {
		if ev.Value == nil {
			l.events[ev.Path] = "<deleted>"
			continue
		}
		l.events[ev.Path] = string(ev.Value)
	}
}

func (l *eventLog) take() map[string]string {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := l.events
	l.events = map[string]string{}
	return out
}

func waitEvents(t *testing.T, l *eventLog, want map[string]string) {
	t.Helper()
	got := map[string]string{}
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		for k, v := range l.take() {
			got[k] = v
		}
		match := true
		for k, v := range want {
			if got[k] != v {
				match = false
			}
		}
		if match {
			for k := range got {
				if _, ok := want[k]; !ok {
					t.Fatalf("unexpected event %s=%s", k, got[k])
				}
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("events = %v, want %v", got, want)
}

func TestSubscriberStoreBacked_DiffsAndSurvivesRestart(t *testing.T) {
	pkA, skA := cipher.GenerateKeyPair()
	_, skB := cipher.GenerateKeyPair()
	dir := t.TempDir()

	pub, err := NewWithTCP("127.0.0.1:0", skA, PubConfig{
		DataDir: filepath.Join(dir, "a"), BatchWindow: 5 * time.Millisecond, NoSyncCXDS: true, KeepRoots: 4,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = pub.Close() }) //nolint:errcheck
	addr := pub.Node().TCP().Address()

	host, err := NewWithTCP("127.0.0.1:0", skB, PubConfig{
		DataDir: filepath.Join(dir, "b"), BatchWindow: 5 * time.Millisecond, NoSyncCXDS: true, KeepRoots: 4,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = host.Close() }) //nolint:errcheck

	open := func() (*Subscriber, *eventLog) {
		sub, err := NewSubscriberOnNode(host.Node(), pkA, SubConfig{StoreBacked: true})
		require.NoError(t, err)
		sub.SetPrefixes([]string{"msgs"})
		l := &eventLog{events: map[string]string{}}
		sub.OnUpdate(l.record)
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		require.NoError(t, sub.ConnectTCP(ctx, addr))
		return sub, l
	}
	sub, events := open()

	for i := range 5 {
		require.NoError(t, pub.Put(fmt.Sprintf("msgs/%d", i), fmt.Appendf(nil, "m%d", i)))
	}
	require.NoError(t, pub.Put("other/x", []byte("filtered")))
	waitEvents(t, events, map[string]string{"msgs/0": "m0", "msgs/1": "m1", "msgs/2": "m2", "msgs/3": "m3", "msgs/4": "m4"})

	require.NoError(t, pub.Put("msgs/1", []byte("edited")))
	require.NoError(t, pub.Delete("msgs/2"))
	waitEvents(t, events, map[string]string{"msgs/1": "edited", "msgs/2": "<deleted>"})

	v, ok := sub.Get("msgs/1")
	require.True(t, ok)
	require.Equal(t, "edited", string(v))
	_, ok = sub.Get("other/x")
	require.False(t, ok, "Get honors the prefix filter")
	var walked []string
	sub.Walk("msgs", func(path string, _ []byte) bool {
		walked = append(walked, path)
		return true
	})
	require.Equal(t, []string{"msgs/0", "msgs/1", "msgs/3", "msgs/4"}, walked)
	size, _ := sub.CacheSizeAndSampleKeys(2)
	require.Equal(t, 4, size)

	// A new subscriber on the same store starts from the last applied Root
	// and reports only what came after it.
	require.NoError(t, sub.Close())
	sub, events = open()
	t.Cleanup(func() { _ = sub.Close() }) //nolint:errcheck
	require.NoError(t, pub.Put("msgs/5", []byte("m5")))
	waitEvents(t, events, map[string]string{"msgs/5": "m5"})
}
