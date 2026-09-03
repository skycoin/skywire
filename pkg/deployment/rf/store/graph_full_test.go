package store

import (
	"context"
	"sort"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/routing"
)

func routeSig(rs []routing.Route) []string {
	out := make([]string, 0, len(rs))
	for _, r := range rs {
		s := ""
		for _, h := range r.Hops {
			s += h.From.Hex()[:6] + "->" + h.To.Hex()[:6] + ";"
		}
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

// TestNewFullGraphMatchesRootGraph: a graph built from GetAllTransports must find
// the same route SET (order-independent) as one explored from the source root.
func TestNewFullGraphMatchesRootGraph(t *testing.T) {
	n1, n2, n3, n4, n5 := generateNodesPK(t)
	m := newMockStore()
	m.SaveEntry(n1, n2, true)
	m.SaveEntry(n1, n4, true)
	m.SaveEntry(n2, n3, true)
	m.SaveEntry(n2, n5, true)
	m.SaveEntry(n3, n5, true)
	m.SaveEntry(n4, n5, true)

	rooted, err := NewGraph(context.Background(), m, n1)
	require.NoError(t, err)
	full, err := NewFullGraph(context.Background(), m)
	require.NoError(t, err)

	for _, maxLen := range []int{2, 3, 100} {
		rr, err1 := rooted.GetRoute(context.Background(), n1, n5, 0, maxLen, 50)
		fr, err2 := full.GetRoute(context.Background(), n1, n5, 0, maxLen, 50)
		require.Equal(t, err1 == nil, err2 == nil, "maxLen=%d err parity", maxLen)
		if err1 == nil {
			require.Equal(t, routeSig(rr), routeSig(fr), "maxLen=%d route sets differ", maxLen)
		}
	}
}
