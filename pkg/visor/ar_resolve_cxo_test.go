package visor

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestARFastResolveServesAPeerOncePerWindow(t *testing.T) {
	var idx arBindingsIndex
	now := time.Now()
	require.True(t, idx.mayServe("pk/stcpr", now))
	require.False(t, idx.mayServe("pk/stcpr", now.Add(time.Second)), "a repeat, likely after a failed dial, goes to the resolver")
	require.True(t, idx.mayServe("pk/squicr", now), "another type is its own lookup")
	require.True(t, idx.mayServe("pk/stcpr", now.Add(fastResolveRepeatAfter)), "the window passes")
}
