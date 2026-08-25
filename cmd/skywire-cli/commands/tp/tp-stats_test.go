// Package clitp cmd/skywire-cli/commands/tp/tp-stats_test.go c4-vis-cli
package clitp

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestCountTransportList covers the client-side fallback path of
// `tp disc -s`: when TPD lacks the /all-transports/stats aggregate endpoint,
// the CLI fetches the full /all-transports list and counts it into the same
// summary shape the endpoint would have returned.
func TestCountTransportList(t *testing.T) {
	// Two stcpr sharing edge "a", one sudph between "c"/"d" — 3 transports,
	// two types, 5 unique visors (a,b1,b2,c,d).
	body := []byte(`[
		{"t_id":"1","edges":["a","b1"],"type":"stcpr"},
		{"t_id":"2","edges":["a","b2"],"type":"stcpr"},
		{"t_id":"3","edges":["c","d"],"type":"sudph"}
	]`)

	ns, err := countTransportList(body)
	require.NoError(t, err)
	require.Equal(t, 3, ns.Total)
	require.Equal(t, 2, ns.ByType["stcpr"])
	require.Equal(t, 1, ns.ByType["sudph"])
	require.Equal(t, 5, ns.UniqueVisors)
}

// TestCountTransportListEmpty confirms an empty list yields zeroed counts
// rather than an error.
func TestCountTransportListEmpty(t *testing.T) {
	ns, err := countTransportList([]byte(`[]`))
	require.NoError(t, err)
	require.Equal(t, 0, ns.Total)
	require.Equal(t, 0, ns.UniqueVisors)
	require.Empty(t, ns.ByType)
}

// TestCountTransportListBadJSON confirms malformed input is reported as an
// error (the caller turns it into a fatal CLI error) rather than panicking.
func TestCountTransportListBadJSON(t *testing.T) {
	_, err := countTransportList([]byte(`not-json`))
	require.Error(t, err)
}
