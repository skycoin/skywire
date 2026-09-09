package visor

import (
	"sync/atomic"
	"testing"
	"time"
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
