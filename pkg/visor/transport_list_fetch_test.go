// Package visor pkg/visor/transport_list_fetch_test.go c3-vis-core
package visor

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/transport"
	tptypes "github.com/skycoin/skywire/pkg/transport/types"
)

func TestTransportListFetcher(t *testing.T) {
	pk, sk := cipher.GenerateKeyPair()
	peer, _ := cipher.GenerateKeyPair()
	e := transport.MakeEntry(pk, peer, tptypes.STCPR, transport.LabelAutomatic)
	l, err := transport.NewSignedList(pk, sk, time.Now().Unix(), []*transport.Entry{&e})
	require.NoError(t, err)
	body, err := json.Marshal(l)
	require.NoError(t, err)

	var hits, notModified atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if r.Header.Get("If-None-Match") == `"`+l.Version()+`"` {
			notModified.Add(1)
			w.WriteHeader(http.StatusNotModified)
			return
		}
		_, _ = w.Write(body) //nolint:errcheck
	}))
	defer srv.Close()

	f := &transportListFetcher{cache: map[cipher.PubKey]*cachedList{}}
	f.client = &http.Client{Transport: &http.Transport{DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, srv.Listener.Addr().String())
	}}}
	ctx := context.Background()

	got, err := f.FetchTransportList(ctx, pk)
	require.NoError(t, err)
	require.Equal(t, l.Version(), got.Version())

	// Fresh: served from the cache.
	_, err = f.FetchTransportList(ctx, pk)
	require.NoError(t, err)
	require.EqualValues(t, 1, hits.Load())

	// Stale: re-checked, and an unchanged list costs a 304.
	f.cache[pk].at = time.Now().Add(-listFreshFor)
	got, err = f.FetchTransportList(ctx, pk)
	require.NoError(t, err)
	require.Equal(t, l.Version(), got.Version())
	require.EqualValues(t, 1, notModified.Load())

	// A list that is not the asked visor's own is refused, and the miss is
	// remembered so the next dial does not wait on it.
	other, _ := cipher.GenerateKeyPair()
	_, err = f.FetchTransportList(ctx, other)
	require.Error(t, err)
	before := hits.Load()
	_, err = f.FetchTransportList(ctx, other)
	require.Error(t, err)
	require.Equal(t, before, hits.Load())
}
