package clisd

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	tptypes "github.com/skycoin/skywire/pkg/transport/types"
)

// Every known type gets its own count, legacy names land on the canonical
// one, and nothing is left hiding in the total.
func TestTransportCountsCoverEveryKnownType(t *testing.T) {
	var c transportCounts
	for _, typ := range tptypes.Known() {
		require.True(t, c.add(typ), typ)
	}
	require.True(t, c.add("quic"), "legacy name")
	require.False(t, c.add("carrier-pigeon"))

	require.Equal(t, len(tptypes.Known())+2, c.Total)
	require.Equal(t, 2, c.SQUICR)
	require.Equal(t, 1, c.Other)
	require.Equal(t, len(tptypes.Known())-1+1, c.direct(), "all known but dmsg, plus the legacy squicr")

	cols := strings.Split(transportColumns(false), "\t")
	require.Len(t, cols, len(tptypes.Known()))
	require.Len(t, strings.Split(c.row(false), "\t"), len(cols))
	require.Len(t, strings.Split(c.row(true), "\t"), len(cols)+1)
}
