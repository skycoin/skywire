// Package uptimestats pkg/uptimestats/uptimestats_test.go
package uptimestats

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
)

// --- filter.go: pure helpers ---

func sv(pk cipher.PubKey, online bool, version string) VisorSummary {
	return VisorSummary{PK: pk, Online: online, Version: version}
}

func TestFilter(t *testing.T) {
	pk1, _ := cipher.GenerateKeyPair()
	pk2, _ := cipher.GenerateKeyPair()
	entries := []VisorSummary{
		sv(pk1, true, "v1.3.0"),
		sv(pk2, false, "v1.2.0"),
	}

	t.Run("zero value matches all", func(t *testing.T) {
		assert.Len(t, Filter(entries, VisorFilter{}), 2)
	})

	t.Run("online only", func(t *testing.T) {
		got := Filter(entries, VisorFilter{OnlineOnly: true})
		require.Len(t, got, 1)
		assert.Equal(t, pk1, got[0].PK)
	})

	t.Run("pk substring", func(t *testing.T) {
		sub := pk2.Hex()[:8]
		got := Filter(entries, VisorFilter{PKSubstr: sub})
		require.Len(t, got, 1)
		assert.Equal(t, pk2, got[0].PK)

		assert.Empty(t, Filter(entries, VisorFilter{PKSubstr: "zzzzzzzz"}))
	})

	t.Run("version equals", func(t *testing.T) {
		got := Filter(entries, VisorFilter{VersionEquals: "1.3.0"})
		require.Len(t, got, 1)
		assert.Equal(t, "v1.3.0", got[0].Version)
	})

	t.Run("min version", func(t *testing.T) {
		got := Filter(entries, VisorFilter{MinVersion: "1.3.0"})
		require.Len(t, got, 1)
		assert.Equal(t, "v1.3.0", got[0].Version)
	})

	t.Run("does not mutate input", func(t *testing.T) {
		_ = Filter(entries, VisorFilter{OnlineOnly: true})
		assert.Len(t, entries, 2)
	})
}

func TestVersionCounts(t *testing.T) {
	pk, _ := cipher.GenerateKeyPair()
	entries := []VisorSummary{
		sv(pk, true, "v1.3.0"),
		sv(pk, true, "v1.3.0"),
		sv(pk, true, "v1.2.0"),
		sv(pk, true, "v1.4.0"),
	}
	got := VersionCounts(entries)
	require.Len(t, got, 3)
	// Highest count first.
	assert.Equal(t, VersionCount{Version: "v1.3.0", Count: 2}, got[0])
	// Ties broken by version ascending.
	assert.Equal(t, "v1.2.0", got[1].Version)
	assert.Equal(t, "v1.4.0", got[2].Version)
}

func TestMinDailyUptime(t *testing.T) {
	pk, _ := cipher.GenerateKeyPair()
	good := VisorSummary{PK: pk, Daily: map[string]string{"2026-06-18": "99", "2026-06-19": "80"}}
	low := VisorSummary{PK: pk, Daily: map[string]string{"2026-06-18": "50"}}
	noDaily := VisorSummary{PK: pk}
	bad := VisorSummary{PK: pk, Daily: map[string]string{"2026-06-18": "not-a-number"}}

	got := MinDailyUptime([]VisorSummary{good, low, noDaily, bad}, 75)
	require.Len(t, got, 1)
	assert.Equal(t, good.Daily, got[0].Daily)
}

func TestNormalizeVersion(t *testing.T) {
	assert.Equal(t, "1.3.0", normalizeVersion("v1.3.0"))
	assert.Equal(t, "1.3.0", normalizeVersion("1.3.0 (abc)"))
	assert.Equal(t, "", normalizeVersion(""))
}

func TestParseSemver(t *testing.T) {
	maj, min, patch, ok := parseSemver("v1.2.3-0")
	assert.True(t, ok)
	assert.Equal(t, 1, maj)
	assert.Equal(t, 2, min)
	assert.Equal(t, 3, patch)

	_, _, _, ok = parseSemver("")
	assert.False(t, ok)
	_, _, _, ok = parseSemver("1.2")
	assert.False(t, ok)
	_, _, _, ok = parseSemver("x.2.3")
	assert.False(t, ok)
	_, _, _, ok = parseSemver("1.y.3")
	assert.False(t, ok)
}

func TestVersionGTE(t *testing.T) {
	assert.True(t, versionGTE("1.3.0", "1.2.0"))  // minor greater
	assert.True(t, versionGTE("2.0.0", "1.9.9"))  // major greater
	assert.True(t, versionGTE("1.2.3", "1.2.3"))  // equal
	assert.False(t, versionGTE("1.2.0", "1.3.0")) // less
	assert.False(t, versionGTE("bad", "1.0.0"))   // unparsable
}

// --- client.go: HTTP fetchers ---

// testServer returns an httptest server replying with status/body and records
// the path+query of the last request it received.
func testServer(t *testing.T, status int, body any) (*httptest.Server, *string) {
	t.Helper()
	lastURL := new(string)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*lastURL = r.URL.String()
		w.WriteHeader(status)
		if s, ok := body.(string); ok {
			_, _ = w.Write([]byte(s)) //nolint
			return
		}
		_ = json.NewEncoder(w).Encode(body) //nolint
	}))
	t.Cleanup(srv.Close)
	return srv, lastURL
}

func TestNewClient(t *testing.T) {
	c := NewClient("https://sd.skycoin.com/", 0)
	assert.Equal(t, "https://sd.skycoin.com", c.baseURL) // trailing slash trimmed
	assert.Equal(t, 30*time.Second, c.http.Timeout)      // zero -> default

	c2 := NewClient("https://x", 5*time.Second)
	assert.Equal(t, 5*time.Second, c2.http.Timeout)
}

func TestVisorUptimes(t *testing.T) {
	pk, _ := cipher.GenerateKeyPair()
	srv, lastURL := testServer(t, http.StatusOK, []VisorSummary{sv(pk, true, "v1.3.0")})
	c := NewClient(srv.URL, time.Second)

	t.Run("v1 no query", func(t *testing.T) {
		out, err := c.VisorUptimes(context.Background(), VisorUptimesOptions{})
		require.NoError(t, err)
		require.Len(t, out, 1)
		assert.Equal(t, pk, out[0].PK)
		assert.Equal(t, "/uptimes", *lastURL)
	})

	t.Run("v2 with visors filter", func(t *testing.T) {
		_, err := c.VisorUptimes(context.Background(), VisorUptimesOptions{
			Version: V2,
			Visors:  []cipher.PubKey{pk},
		})
		require.NoError(t, err)
		assert.Contains(t, *lastURL, "v=v2")
		assert.Contains(t, *lastURL, "visors="+pk.Hex())
	})
}

func TestTransportUptimes(t *testing.T) {
	srv, lastURL := testServer(t, http.StatusOK, []TransportSummary{{ID: uuid.New(), Online: true, Type: "stcpr"}})
	c := NewClient(srv.URL, time.Second)

	out, err := c.TransportUptimes(context.Background(), V3)
	require.NoError(t, err)
	require.Len(t, out, 1)
	assert.Equal(t, "stcpr", out[0].Type)
	assert.Contains(t, *lastURL, "/uptimes/transports")
	assert.Contains(t, *lastURL, "v=v3")
}

func TestNetworkUptime(t *testing.T) {
	want := NetworkUptime{TotalTransports: 100, Online: 60, ByType: map[string]TypeBreakdown{"stcpr": {Total: 40, Online: 20}}}
	srv, lastURL := testServer(t, http.StatusOK, want)
	c := NewClient(srv.URL, time.Second)

	out, err := c.NetworkUptime(context.Background())
	require.NoError(t, err)
	assert.Equal(t, want.TotalTransports, out.TotalTransports)
	assert.Equal(t, want.ByType["stcpr"], out.ByType["stcpr"])
	assert.Equal(t, "/metrics/uptime", *lastURL)
}

func TestTransportUptimeForIDs(t *testing.T) {
	id := uuid.New()
	srv, lastURL := testServer(t, http.StatusOK, []TransportSummary{{ID: id}})
	c := NewClient(srv.URL, time.Second)

	t.Run("empty ids", func(t *testing.T) {
		_, err := c.TransportUptimeForIDs(context.Background(), nil)
		assert.Error(t, err)
	})

	t.Run("with ids", func(t *testing.T) {
		out, err := c.TransportUptimeForIDs(context.Background(), []uuid.UUID{id})
		require.NoError(t, err)
		require.Len(t, out, 1)
		assert.Contains(t, *lastURL, "/metrics/uptime/"+id.String())
	})
}

func TestTransportUptimeForVisors(t *testing.T) {
	pk, _ := cipher.GenerateKeyPair()
	srv, lastURL := testServer(t, http.StatusOK, []TransportSummary{})
	c := NewClient(srv.URL, time.Second)

	t.Run("empty pks", func(t *testing.T) {
		_, err := c.TransportUptimeForVisors(context.Background(), nil)
		assert.Error(t, err)
	})

	t.Run("with pks", func(t *testing.T) {
		_, err := c.TransportUptimeForVisors(context.Background(), []cipher.PubKey{pk})
		require.NoError(t, err)
		assert.Contains(t, *lastURL, "/metrics/uptime/visor/"+pk.Hex())
	})
}

func TestGetJSON_Errors(t *testing.T) {
	t.Run("non-200 status", func(t *testing.T) {
		srv, _ := testServer(t, http.StatusNotFound, "not found")
		c := NewClient(srv.URL, time.Second)
		_, err := c.NetworkUptime(context.Background())
		require.Error(t, err)
		assert.Contains(t, err.Error(), "HTTP 404")
		assert.Contains(t, err.Error(), "not found")
	})

	t.Run("invalid json body", func(t *testing.T) {
		srv, _ := testServer(t, http.StatusOK, "{not json")
		c := NewClient(srv.URL, time.Second)
		_, err := c.NetworkUptime(context.Background())
		assert.Error(t, err)
	})

	t.Run("transport error / unreachable", func(t *testing.T) {
		// Point at a closed server so the request fails at the transport layer.
		srv, _ := testServer(t, http.StatusOK, "")
		url := srv.URL
		srv.Close()
		c := NewClient(url, 200*time.Millisecond)
		_, err := c.VisorUptimes(context.Background(), VisorUptimesOptions{})
		assert.Error(t, err)
	})

	t.Run("canceled context", func(t *testing.T) {
		srv, _ := testServer(t, http.StatusOK, []VisorSummary{})
		c := NewClient(srv.URL, time.Second)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, err := c.VisorUptimes(ctx, VisorUptimesOptions{})
		assert.Error(t, err)
	})
}
