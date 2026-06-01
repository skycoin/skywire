package rfclient

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/routing"
)

// TestFindRoutesRequest_NumRoutesRoundTrip guards the wire contract
// between the rfclient (marshals FindRoutesRequest) and the
// route-finder API (unmarshals the same struct): the NumRoutes field
// must survive JSON so a multiplexed dial's requested route count
// actually reaches the finder. A missing/renamed json tag would
// silently drop it and re-cap the finder at its default of 3.
func TestFindRoutesRequest_NumRoutesRoundTrip(t *testing.T) {
	a, _ := cipher.GenerateKeyPair()
	b, _ := cipher.GenerateKeyPair()

	req := &FindRoutesRequest{
		Edges: []routing.PathEdges{{a, b}},
		Opts:  &RouteOptions{MinHops: 2, MaxHops: 7, NumRoutes: 10},
	}
	raw, err := json.Marshal(req)
	require.NoError(t, err)

	var got FindRoutesRequest
	require.NoError(t, json.Unmarshal(raw, &got))
	require.NotNil(t, got.Opts)
	assert.Equal(t, uint16(2), got.Opts.MinHops)
	assert.Equal(t, uint16(7), got.Opts.MaxHops)
	assert.Equal(t, uint16(10), got.Opts.NumRoutes)
}

// TestRouteOptions_ZeroNumRoutesOmittedDefault confirms the zero value
// round-trips as zero (the API treats 0 as "use the service default"),
// so pre-NumRoutes callers and non-mux dials keep the old behavior.
func TestRouteOptions_ZeroNumRoutesDefault(t *testing.T) {
	raw, err := json.Marshal(&RouteOptions{MinHops: 0, MaxHops: 5})
	require.NoError(t, err)
	var got RouteOptions
	require.NoError(t, json.Unmarshal(raw, &got))
	assert.Equal(t, uint16(0), got.NumRoutes)
}
