package clitp

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/transport"
	tptypes "github.com/skycoin/skywire/pkg/transport/types"
	"github.com/skycoin/skywire/pkg/visor/visorapi"
)

type skynetFake struct {
	visorapi.API
	body []byte
}

func (f skynetFake) SkynetHTTP(visorapi.SkynetHTTPRequest) (*visorapi.SkynetHTTPResponse, error) {
	return &visorapi.SkynetHTTPResponse{StatusCode: http.StatusOK, Body: f.body}, nil
}

// The list is accepted only when it is the asked visor's and its signature
// holds; the IDs come from the edges and type.
func TestFetchSkynetList(t *testing.T) {
	pk, sk := cipher.GenerateKeyPair()
	remote, _ := cipher.GenerateKeyPair()
	l, err := transport.NewSignedList(pk, sk, 1, []*transport.Entry{{Edges: transport.SortEdges(pk, remote), Type: tptypes.STCPR}})
	require.NoError(t, err)
	body, err := json.Marshal(l)
	require.NoError(t, err)

	got, err := fetchSkynetList(skynetFake{body: body}, pk)
	require.NoError(t, err)
	require.Len(t, got.Transports(), 1)
	require.Equal(t, transport.MakeTransportID(pk, remote, tptypes.STCPR), got.Transports()[0].ID)

	other, _ := cipher.GenerateKeyPair()
	_, err = fetchSkynetList(skynetFake{body: body}, other)
	require.Error(t, err, "another visor's list")

	l.At = 2
	tampered, err := json.Marshal(l)
	require.NoError(t, err)
	_, err = fetchSkynetList(skynetFake{body: tampered}, pk)
	require.Error(t, err, "a list changed after signing")
}
