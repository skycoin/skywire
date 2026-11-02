// Package api pkg/dmsg/discovery/api/inbox_test.go c1-net-dmsg
package api

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/disc/metrics"
	"github.com/skycoin/skywire/pkg/dmsg/discovery/store"
	"github.com/skycoin/skywire/pkg/dmsg/dmsgasync"
)

// A recipient publishes its inbox record and a sender reads it back through
// the dmsgasync helpers. Forged, misaddressed and older records are refused.
func TestInboxEndpoint(t *testing.T) {
	api := New(nil, store.NewMock(), metrics.NewEmpty(), true, false, true, "", "", 0)
	srv := httptest.NewServer(api.Handler)
	t.Cleanup(srv.Close)
	ctx := context.Background()
	hc := srv.Client()

	pk, sk := cipher.GenerateKeyPair()
	host, _ := cipher.GenerateKeyPair()

	_, err := dmsgasync.FetchRecord(ctx, hc, srv.URL, pk)
	require.ErrorIs(t, err, dmsgasync.ErrNoRecord)

	v2, err := dmsgasync.NewRecord(pk, sk, []cipher.PubKey{host}, 4096, 2, time.Hour)
	require.NoError(t, err)
	require.NoError(t, dmsgasync.PublishRecord(ctx, hc, srv.URL, v2))

	got, err := dmsgasync.FetchRecord(ctx, hc, srv.URL, pk)
	require.NoError(t, err)
	require.Equal(t, uint64(2), got.Seq)
	require.Equal(t, []cipher.PubKey{host}, got.Hosts)

	v1, err := dmsgasync.NewRecord(pk, sk, []cipher.PubKey{host}, 4096, 1, time.Hour)
	require.NoError(t, err)
	require.Error(t, dmsgasync.PublishRecord(ctx, hc, srv.URL, v1), "an older record is refused")

	_, otherSK := cipher.GenerateKeyPair()
	forged := v2
	forged.Seq = 3
	require.NoError(t, forged.Sign(otherSK))
	require.Error(t, dmsgasync.PublishRecord(ctx, hc, srv.URL, forged), "a record signed by another key is refused")

	// A record posted under another key's path is refused.
	body := []byte(`{"pk":"` + pk.Hex() + `"}`)
	other, _ := cipher.GenerateKeyPair()
	resp, err := hc.Post(srv.URL+"/dmsg-discovery/inbox/"+other.Hex(), "application/json", bytes.NewReader(body))
	require.NoError(t, err)
	_ = resp.Body.Close() //nolint:errcheck
	require.Equal(t, http.StatusUnprocessableEntity, resp.StatusCode)

	got, err = dmsgasync.FetchRecord(ctx, hc, srv.URL, pk)
	require.NoError(t, err)
	require.Equal(t, uint64(2), got.Seq, "the stored record is unchanged")
}
