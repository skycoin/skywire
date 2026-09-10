// Package visorcore pkg/visor/visorcore/autoconnect_backoff_test.go c3-vis-core
package visorcore

import (
	"testing"
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
	tptypes "github.com/skycoin/skywire/pkg/transport/types"
)

func TestDialBackoffSchedule(t *testing.T) {
	want := []time.Duration{5 * time.Minute, 10 * time.Minute, 20 * time.Minute, 40 * time.Minute, 80 * time.Minute, 2 * time.Hour, 2 * time.Hour}
	for i, w := range want {
		if got := dialBackoff(i + 1); got != w {
			t.Fatalf("dialBackoff(%d) = %v, want %v", i+1, got, w)
		}
	}
}

func TestConnectorBackoffLifecycle(t *testing.T) {
	c := &Connector{}
	pk, _ := cipher.GenerateKeyPair()
	now := time.Now()
	if c.inBackoff(pk, tptypes.STCPR, now) {
		t.Fatal("fresh target must not be in backoff")
	}
	if w := c.noteFailure(pk, tptypes.STCPR, now); w != dialBackoffMin {
		t.Fatalf("first failure wait %v", w)
	}
	if !c.inBackoff(pk, tptypes.STCPR, now.Add(time.Minute)) {
		t.Fatal("target must be skipped inside the wait")
	}
	if c.inBackoff(pk, tptypes.SUDPH, now.Add(time.Minute)) {
		t.Fatal("backoff is per transport type")
	}
	if c.inBackoff(pk, tptypes.STCPR, now.Add(dialBackoffMin+time.Second)) {
		t.Fatal("target must be retried after the wait")
	}
	if w := c.noteFailure(pk, tptypes.STCPR, now.Add(dialBackoffMin+time.Second)); w != 2*dialBackoffMin {
		t.Fatalf("second failure wait %v", w)
	}
	if c.BackedOff(now.Add(dialBackoffMin+2*time.Second)) != 1 {
		t.Fatal("one pair should be backed off")
	}
	c.noteSuccess(pk, tptypes.STCPR)
	if c.inBackoff(pk, tptypes.STCPR, now.Add(dialBackoffMin+2*time.Second)) || c.BackedOff(now) != 0 {
		t.Fatal("success must clear the backoff")
	}
}
