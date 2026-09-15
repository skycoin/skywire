// Package visor pkg/visor/absent_hypervisor_warn_test.go c3-vis-core
package visor

import "testing"

// TestAbsentWarnAfterIsPastTheBackoffCeiling. The one-shot operator notice for a
// hypervisor key that resolves to nothing must not fire while the peer could
// still plausibly come back — a desk tab mid-reload looks exactly like an absent
// one for a few seconds.
//
// absentDialBackoff doubles from 1s to a 1-minute ceiling, so by the time this
// many consecutive absences have accrued the visor has been waiting minutes, not
// seconds. Below the ceiling the notice would be noise on an ordinary reload;
// far above it the stale key stays invisible, which is the bug: on one visor
// three dead keys accounted for 185 of ~200 dial failures in a 2000-line sample
// with nothing in the log naming them.
func TestAbsentWarnAfterIsPastTheBackoffCeiling(t *testing.T) {
	if absentDialBackoff(absentWarnAfter) != rpcAbsentMaxBackoff {
		t.Errorf("by attempt %d the backoff is %v, not the %v ceiling — the notice "+
			"can fire while a peer is still plausibly returning",
			absentWarnAfter, absentDialBackoff(absentWarnAfter), rpcAbsentMaxBackoff)
	}
	// And it must not be so late that the operator never sees it. Ten attempts
	// at a 1-minute ceiling is a few minutes; a hundred would be over an hour.
	if absentWarnAfter > 30 {
		t.Errorf("absentWarnAfter = %d is too late to be an operator signal", absentWarnAfter)
	}
}
