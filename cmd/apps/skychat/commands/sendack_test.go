// Package commands cmd/apps/skychat/commands/sendack_test.go
//
// What remains of the ack surface in the app: the /message wait_ms clamp. The
// envelope codec, the ack-waiter map and the inbound-envelope decode moved into
// the shared controller when the DM core was extracted — their coverage lives
// in pkg/skychat/dm (controller_test.go, controller_receipts_test.go).
package commands

import (
	"testing"
	"time"
)

func TestClampAckWait(t *testing.T) {
	cases := []struct {
		name string
		in   time.Duration
		want time.Duration
	}{
		{"zero -> floor", 0, chatAckTimeoutFloor},
		{"negative -> floor", -5 * time.Second, chatAckTimeoutFloor},
		{"below floor -> floor", 50 * time.Millisecond, chatAckTimeoutFloor},
		{"at floor -> floor", chatAckTimeoutFloor, chatAckTimeoutFloor},
		{"mid unchanged", 5 * time.Second, 5 * time.Second},
		{"at ceiling -> ceiling", chatAckTimeoutCeiling, chatAckTimeoutCeiling},
		{"above ceiling -> ceiling", 2 * time.Minute, chatAckTimeoutCeiling},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := clampAckWait(c.in); got != c.want {
				t.Errorf("clampAckWait(%v) = %v, want %v", c.in, got, c.want)
			}
		})
	}
}
