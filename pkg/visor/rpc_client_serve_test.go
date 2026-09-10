package visor

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
	dmsgdisc "github.com/skycoin/skywire/pkg/dmsg/disc"
)

func TestShouldUpgradeToSkynet(t *testing.T) {
	var never atomic.Int64
	if shouldUpgradeToSkynet(false, &never) {
		t.Fatal("no direct transport must not trigger an upgrade")
	}
	if !shouldUpgradeToSkynet(true, &never) {
		t.Fatal("a direct transport with no recorded failure must trigger an upgrade")
	}

	var recent atomic.Int64
	recent.Store(time.Now().UnixNano())
	if shouldUpgradeToSkynet(true, &recent) {
		t.Fatal("a skynet failure inside the cooldown must hold the dmsg conn")
	}

	var stale atomic.Int64
	stale.Store(time.Now().Add(-rpcSkynetCooldown - time.Second).UnixNano())
	if !shouldUpgradeToSkynet(true, &stale) {
		t.Fatal("a failure older than the cooldown must allow the upgrade")
	}
}

func TestIsPeerAbsent(t *testing.T) {
	if !isPeerAbsent(dmsgdisc.ErrKeyNotFound) {
		t.Fatal("sentinel not matched")
	}
	if !isPeerAbsent(fmt.Errorf("dial: %w", dmsgdisc.ErrKeyNotFound)) {
		t.Fatal("wrapped sentinel not matched")
	}
	if !isPeerAbsent(errors.New("dmsg: lookup: " + dmsgdisc.ErrKeyNotFound.Error())) {
		t.Fatal("string-wrapped sentinel not matched")
	}
	if isPeerAbsent(errors.New("connection refused")) || isPeerAbsent(nil) {
		t.Fatal("unrelated error or nil treated as absent")
	}
}

func TestDialBackoffs(t *testing.T) {
	if nextDialBackoff(0) != rpcDialInitBackoff {
		t.Fatalf("fresh transient backoff = %v", nextDialBackoff(0))
	}
	d := time.Duration(0)
	for i := 0; i < 20; i++ {
		d = nextDialBackoff(d)
	}
	if d != rpcDialMaxBackoff {
		t.Fatalf("transient backoff did not cap: %v", d)
	}
	want := []time.Duration{time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second, 16 * time.Second, 32 * time.Second, time.Minute, time.Minute}
	for i, w := range want {
		if got := absentDialBackoff(i + 1); got != w {
			t.Fatalf("absentDialBackoff(%d) = %v, want %v", i+1, got, w)
		}
	}
}

// waitForRedial honors the deadline with no transport manager and returns
// false as soon as the context ends.
func TestWaitForRedial(t *testing.T) {
	var pk cipher.PubKey
	start := time.Now()
	if !waitForRedial(context.Background(), 20*time.Millisecond, nil, pk) {
		t.Fatal("deadline should report true")
	}
	if time.Since(start) < 20*time.Millisecond {
		t.Fatal("returned before the deadline")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if waitForRedial(ctx, time.Hour, nil, pk) {
		t.Fatal("canceled context should report false")
	}
}
