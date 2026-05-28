// Package policy pkg/router/policy/bench_test.go — perf budget
// regression test for the Indonesia-Friday policy. The RFC's
// per-dial budget is 50ms; this benchmark confirms we have
// comfortable margin (target: <1ms on ARM, sub-100µs on amd64).
package policy

import (
	"context"
	"testing"
)

func BenchmarkFridayPolicy(b *testing.B) {
	clock := &fakeClock{now: fridayAt(17)}
	eval, err := NewEvaluator("bench.star", indonesiaFridayPolicy, WithClock(clock))
	if err != nil {
		b.Fatal(err)
	}
	ctx := context.Background()
	rctx := RoutingContext{App: "skychat"}
	cs := []Candidate{candidateThroughID, candidateThroughUSFastest, candidateThroughEU}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = eval.Decide(ctx, rctx, cs) //nolint:errcheck
	}
}
