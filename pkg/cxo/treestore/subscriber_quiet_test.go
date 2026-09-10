// Package treestore pkg/cxo/treestore/subscriber_quiet_test.go c3-cxo-tree
package treestore

import (
	"testing"
	"time"
)

func TestQuietThresholdBacksOff(t *testing.T) {
	base := 60 * time.Second
	want := []time.Duration{60 * time.Second, 2 * time.Minute, 4 * time.Minute, 8 * time.Minute, 15 * time.Minute, 15 * time.Minute}
	for streak, w := range want {
		if got := quietThresholdFor(base, int64(streak)); got != w {
			t.Fatalf("streak %d: %v, want %v", streak, got, w)
		}
	}
}
