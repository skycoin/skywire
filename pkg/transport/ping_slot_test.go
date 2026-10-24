package transport

import (
	"testing"

	"github.com/google/uuid"
)

// TestPingSlotSpreadsTransports: a few hundred transports land in every slot
// of the interval, none far above the average, and each one always lands in
// the same slot so its own interval is kept.
func TestPingSlotSpreadsTransports(t *testing.T) {
	const n, slots = 400, 20
	counts := make([]int, slots)
	for i := 0; i < n; i++ {
		id := uuid.New()
		s := pingSlot(id, slots)
		if s != pingSlot(id, slots) {
			t.Fatal("a transport's slot changed between calls")
		}
		counts[s]++
	}
	for s, c := range counts {
		if c == 0 || c > 3*n/slots {
			t.Errorf("slot %d holds %d of %d transports, want about %d", s, c, n, n/slots)
		}
	}
}
