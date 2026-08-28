// Package router leg_dataprogress_test.go
package router

import (
	"testing"

	"github.com/google/uuid"
)

// TestSelectDataStalledLegs exercises the fast data-progress prune decision: a
// leg delivering a trickle (< 1/16 of the fastest active leg) while the receiver
// is stuck on a reorder gap is black-holing its share and gets pruned, without
// ever dropping the last active leg, and standby legs are never touched.
func TestSelectDataStalledLegs(t *testing.T) {
	fast := uuid.New()
	slowA := uuid.New()
	slowB := uuid.New()
	spare := uuid.New()

	// contains reports whether got holds exactly the wantIDs (order-independent).
	contains := func(got []uuid.UUID, want ...uuid.UUID) bool {
		if len(got) != len(want) {
			return false
		}
		seen := map[uuid.UUID]bool{}
		for _, g := range got {
			seen[g] = true
		}
		for _, w := range want {
			if !seen[w] {
				return false
			}
		}
		return true
	}

	t.Run("prunes trickle legs while gap stuck", func(t *testing.T) {
		legs := []legRecvDelta{
			{id: fast, delta: 16000},
			{id: slowA, delta: 100}, // < 16000/16 = 1000
			{id: slowB, delta: 0},
		}
		got := selectDataStalledLegs(legs, true)
		if !contains(got, slowA, slowB) {
			t.Fatalf("got %v, want the two trickle legs", got)
		}
	})

	t.Run("no prune when gap not stuck and leader is slow", func(t *testing.T) {
		// Leader moved only 16000B over the window (< legBlackHoleMinTopBytes), so
		// the frontier-healthy black-hole path does not judge — the whole group is
		// merely slow, not one leg black-holing a fast one.
		legs := []legRecvDelta{
			{id: fast, delta: 16000},
			{id: slowA, delta: 0},
		}
		if got := selectDataStalledLegs(legs, false); got != nil {
			t.Fatalf("got %v, want nil (gap not stuck, leader below floor)", got)
		}
	})

	t.Run("prunes a pure black-hole with a fast leader even when gap not stuck", func(t *testing.T) {
		// Leader moved 2MB over the window; slowA delivered < 1/64 of it while
		// still counting as active — a normal-latency goodput black-hole the
		// latency band would miss. Shed it without waiting for a frontier stall.
		legs := []legRecvDelta{
			{id: fast, delta: 2 * 1024 * 1024},
			{id: slowA, delta: 500},
		}
		got := selectDataStalledLegs(legs, false)
		if !contains(got, slowA) {
			t.Fatalf("got %v, want the black-hole leg pruned without a stuck gap", got)
		}
	})

	t.Run("keeps a leg pulling its weight when gap not stuck", func(t *testing.T) {
		// slowA carries ~10% of a fast leader — a real contributor, not a
		// black-hole — so a healthy frontier leaves it alone.
		legs := []legRecvDelta{
			{id: fast, delta: 2 * 1024 * 1024},
			{id: slowA, delta: 200 * 1024},
		}
		if got := selectDataStalledLegs(legs, false); got != nil {
			t.Fatalf("got %v, want nil (leg is contributing)", got)
		}
	})

	t.Run("no prune when all legs comparable", func(t *testing.T) {
		legs := []legRecvDelta{
			{id: fast, delta: 1000},
			{id: slowA, delta: 900},
			{id: slowB, delta: 950},
		}
		if got := selectDataStalledLegs(legs, true); got != nil {
			t.Fatalf("got %v, want nil (converged set, no outlier)", got)
		}
	})

	t.Run("never prunes the last active leg", func(t *testing.T) {
		// Only two active legs, both trickle relative to... there is no faster
		// one, so top is the max of the two; keep at least one regardless.
		legs := []legRecvDelta{
			{id: fast, delta: 8000},
			{id: slowA, delta: 0},
			{id: slowB, delta: 0},
		}
		got := selectDataStalledLegs(legs, true)
		// slowA and slowB both qualify (0 <= 8000/16); active=3 so pruning 2 keeps 1.
		if len(got) != 2 {
			t.Fatalf("got %v, want 2 pruned (one active leg kept)", got)
		}
	})

	t.Run("standby legs are never pruned", func(t *testing.T) {
		legs := []legRecvDelta{
			{id: fast, delta: 16000},
			{id: slowA, delta: 100},
			{id: spare, delta: 0, standby: true}, // parked; zero recv is expected
		}
		got := selectDataStalledLegs(legs, true)
		if !contains(got, slowA) {
			t.Fatalf("got %v, want only the active trickle leg (never the standby)", got)
		}
	})

	t.Run("no prune when group is idle", func(t *testing.T) {
		legs := []legRecvDelta{
			{id: fast, delta: 0},
			{id: slowA, delta: 0},
		}
		if got := selectDataStalledLegs(legs, true); got != nil {
			t.Fatalf("got %v, want nil (group not moving data)", got)
		}
	})

	t.Run("single active leg is left alone", func(t *testing.T) {
		legs := []legRecvDelta{
			{id: fast, delta: 5000},
		}
		if got := selectDataStalledLegs(legs, true); got != nil {
			t.Fatalf("got %v, want nil (never risk the only leg)", got)
		}
	})
}
