// Package skysocks pkg/skysocks/spread_degraded_test.go c4-app-proxy
package skysocks

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/skysocks/skysettings"
)

// A session that came up on FEWER routes than it asked for is degraded, not
// broken: it must still serve, and the spread floor must never hold a byte back
// waiting for routes that do not exist.
//
// This is the client half of the `proxy start` fix. The app now reports Running
// on its first tunnel and widens toward --tunnels in the background, so a
// topology with one route to the exit (the three-visor docker e2e, a two-node
// lab) leaves a client whose target is 2 and whose live width is 1 — with
// spread.min_routes asking for 3 on top of that. ensureMinRoutes NEVER dials:
// it promotes what the pool holds and reports the width it reached, and the
// planner runs the object on that width. Nothing blocks, nothing errors, and
// every chunk goes out over the one route there is.
func TestSpreadMinRoutesDoesNotStallASingleRouteSession(t *testing.T) {
	t.Cleanup(func() { skysettings.Reset() })

	const blobSize = 4 << 20
	blob := make([]byte, blobSize)
	for i := range blob {
		blob[i] = byte(i*17 + 3)
	}
	want := sha256.Sum256(blob)

	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Etag", "\"degradedv1\"")
		http.ServeContent(w, r, "blob.bin", time.Unix(0, 0), bytes.NewReader(blob))
	}))
	defer backend.Close()

	const chunk = int64(256 << 10)
	// One active tunnel, nothing in the pool: the single dialable route.
	proxy, client := newSpreadTestClient(t, backend.Listener.Addr().String(), 1, 0, 6, chunk)
	// The app asked for two. The second never dialed; the Client keeps reaching
	// for it in the background and serves on the one it has meanwhile.
	client.SetTunnelTarget(2)

	require.True(t, skysettings.Apply(map[string]int64{
		skysettings.SpreadMaxShare:  mustRatio(t),
		skysettings.SpreadMinRoutes: 3,
		skysettings.ChunkMaxBytes:   chunk,
		skysettings.ChunkProbeBytes: chunk,
	}))

	// The floor is honest about what it reached and dials nothing to get there.
	require.Equal(t, 1, client.ensureMinRoutes(3, spreadDown, "test"),
		"the floor must report the width that exists, not wait for one that does not")
	require.Len(t, client.snapshotSessions(), 1, "ensureMinRoutes must never add a tunnel")

	resp := socks5Get(t, proxy, "/blob.bin")
	defer resp.Body.Close() //nolint:errcheck
	require.Equal(t, 200, resp.StatusCode)
	got, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.Len(t, got, blobSize, "every chunk of the object was placed on the one live route")
	require.Equal(t, want, sha256.Sum256(got), "the degraded session must still deliver the object byte-identically")
}

// The same floor on the upload half: a striped POST over one route places every
// The same floor on the upload half: a striped POST over ONE route places every
// chunk rather than stalling on a min_routes that cannot be met.
func TestSpreadMinRoutesDoesNotStallASingleRouteUpload(t *testing.T) {
	t.Cleanup(func() { skysettings.Reset() })
	defer restoreUploadTunables(uploadChunkBytes, uploadMemBytes, uploadStripeMinBytes, uploadConcurrency)()
	uploadChunkBytes = 256 << 10
	uploadMemBytes = 4 << 20
	uploadStripeMinBytes = 256 << 10
	uploadConcurrency = 2

	const blobSize = 2 << 20
	blob := make([]byte, blobSize)
	for i := range blob {
		blob[i] = byte(i*29 + 11)
	}
	want := sha256.Sum256(blob)

	sink := &stubSink{}
	backend := httptest.NewServer(sink.handler())
	defer backend.Close()

	proxy, client := newSpreadTestClient(t, backend.Listener.Addr().String(), 1, 0, 4, 256<<10)
	client.SetTunnelTarget(2)
	require.True(t, skysettings.Apply(map[string]int64{
		skysettings.SpreadMaxShare:  mustRatio(t),
		skysettings.SpreadMinRoutes: 3,
	}))

	resp := socks5Upload(t, proxy, blob)
	defer resp.Body.Close() //nolint:errcheck
	require.Equal(t, 200, resp.StatusCode)
	var got struct {
		Bytes  int64  `json:"bytes"`
		Sha256 string `json:"sha256"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&got))
	require.EqualValues(t, blobSize, got.Bytes, "every chunk reached the sink over the one live route")
	require.Equal(t, hex.EncodeToString(want[:]), got.Sha256)
}
