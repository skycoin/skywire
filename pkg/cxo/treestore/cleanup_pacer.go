// Package treestore pkg/cxo/treestore/cleanup_pacer.go c2-net-cxo
package treestore

import "time"

// cleanupDutyFactor bounds the share of wall time the publisher's cleanup
// sweep may occupy: after a sweep that took T, the next nudge-driven sweep
// is held until cleanupDutyFactor*T has passed since it started, so the
// sweep never takes more than ~1/cleanupDutyFactor of the process's time.
// A sweep over a few-MB store takes milliseconds and is effectively
// unpaced; one over a multi-GB store is spread out instead of running
// back-to-back on every publish. The forced periodic sweep is not paced.
const cleanupDutyFactor = 20

// cleanupPacer remembers when the last sweep started and how long it took.
type cleanupPacer struct {
	lastStart time.Time
	lastTook  time.Duration
}

// ran records a completed sweep.
func (c *cleanupPacer) ran(start time.Time, took time.Duration) {
	c.lastStart, c.lastTook = start, took
}

// holdFor returns how much longer a nudge arriving at now must wait before
// the next sweep may start; zero means run now.
func (c *cleanupPacer) holdFor(now time.Time) time.Duration {
	if c.lastStart.IsZero() {
		return 0
	}
	next := c.lastStart.Add(c.lastTook * cleanupDutyFactor)
	if d := next.Sub(now); d > 0 {
		return d
	}
	return 0
}
