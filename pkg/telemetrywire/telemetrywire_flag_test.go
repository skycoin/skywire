package telemetrywire

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// The same-network bit rides the type byte: it survives a shard round trip,
// and readers still get the type from a flagged code.
func TestTypeFlagSameNetwork(t *testing.T) {
	flagged := TypeSTCPR | TypeFlagSameNetwork
	require.True(t, SameNetwork(flagged))
	require.False(t, SameNetwork(TypeSTCPR))
	require.Equal(t, CodeToType(TypeSTCPR), CodeToType(flagged))

	id := uuid.New()
	sh := ShardOf(id)
	_, got, err := DecodeShard(EncodeShard(sh, []Entry{{ID: id, SentBytes: 7, Type: flagged}}))
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.True(t, SameNetwork(got[0].Type))
	require.Equal(t, "stcpr", CodeToType(got[0].Type))
}
