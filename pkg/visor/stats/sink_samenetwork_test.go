package stats

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/telemetrywire"
)

// A same-network transport goes out with the flag on its type; a transport of
// unknown type never carries the flag alone.
func TestSnapshotToEntrySameNetwork(t *testing.T) {
	id := uuid.New()
	e := snapshotToEntry(id, &LiveSnapshot{Type: "stcpr", SameNetwork: true})
	require.True(t, telemetrywire.SameNetwork(e.Type))
	require.Equal(t, "stcpr", telemetrywire.CodeToType(e.Type))

	require.False(t, telemetrywire.SameNetwork(snapshotToEntry(id, &LiveSnapshot{Type: "stcpr"}).Type))
	require.Equal(t, telemetrywire.TypeUnknown, snapshotToEntry(id, &LiveSnapshot{SameNetwork: true}).Type)
}
