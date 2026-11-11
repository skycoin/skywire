package ui2

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMarkOutdated(t *testing.T) {
	ns := []Node{
		{PK: "a", Version: "v1.3.104"},
		{PK: "b", Version: "v1.3.104-12-gabc+dirty", Children: []Node{{PK: "c", Version: "v1.3.103"}, {PK: "d", Version: "unknown"}}},
		{PK: "e", Version: "v1.3.104-12-gdef"},
	}
	MarkOutdated(ns)
	require.True(t, ns[0].Outdated)
	require.False(t, ns[1].Outdated)
	require.True(t, ns[1].Children[0].Outdated)
	require.False(t, ns[1].Children[1].Outdated, "a version that does not parse is never outdated")
	require.False(t, ns[2].Outdated)
	require.Less(t, ns[1].Children[0].VersionKey(), ns[0].VersionKey())
	require.Less(t, ns[0].VersionKey(), ns[1].VersionKey())
}
