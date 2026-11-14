package ui2

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestServicesHelpers(t *testing.T) {
	require.Equal(t, "dmsg://02ab:80/health", EndpointURL("dmsg://02ab:80/"))
	require.Equal(t, "http://x/Health", EndpointURL("http://x/Health"))
	require.Equal(t, "tpd", SvcKey("Transport Discovery"))
	require.Equal(t, "", SvcKey("DMSG Server"))
	got := TopFailures(map[string]uint64{"a": 1, "b": 5, "c": 0, "d": 3, "e": 2})
	require.Equal(t, []RSNFailure{{"b", 5}, {"d", 3}, {"e", 2}}, got)
	require.True(t, Services{Entries: nil}.AnyDown() == false)
}
