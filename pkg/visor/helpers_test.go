// Package visor pkg/visor/helpers_test.go: unit tests for the small,
// dependency-free helper functions scattered across the package (CXO
// feed name mapping, address/port parsing, hop conversion, etc.). The
// networked runtime/RPC code is exercised by the integration suite.
package visor

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ccding/go-stun/stun"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/app/appserver"
	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/router"
	"github.com/skycoin/skywire/pkg/visor/stats"
)

func TestCXOFeedStringRoundTrip(t *testing.T) {
	feeds := []CXOFeed{
		FeedTPDMetrics, FeedTPDUptime, FeedSDServices,
		FeedDMSGDClientsByServer, FeedTPDAllTransports,
	}
	for _, f := range feeds {
		name := CXOFeedString(f)
		require.NotEmpty(t, name)
		got, ok := CXOFeedFromString(name)
		require.True(t, ok, "round-trip %q", name)
		require.Equal(t, f, got)
	}

	// Unknown name → (0, false); unknown feed → "feed#N".
	_, ok := CXOFeedFromString("nope")
	require.False(t, ok)
	require.Contains(t, CXOFeedString(CXOFeed(99)), "feed#")
}

func TestExtractPort(t *testing.T) {
	require.Equal(t, 0, extractPort(""))
	require.Equal(t, 8080, extractPort("1.2.3.4:8080")) // host:port
	require.Equal(t, 9090, extractPort("9090"))         // bare port
	require.Equal(t, 0, extractPort("not-a-port"))      // unparseable
}

func TestDaysFromPath(t *testing.T) {
	require.Equal(t, 7, daysFromPath("uptimes/days/7", "uptimes/days/"))
	require.Equal(t, 0, daysFromPath("other/3", "uptimes/days/"))        // wrong prefix
	require.Equal(t, 0, daysFromPath("uptimes/days/x", "uptimes/days/")) // non-digit
	require.Equal(t, 0, daysFromPath("uptimes/days/", "uptimes/days/"))  // empty rest → prefix == path
}

func TestAppsContains(t *testing.T) {
	apps := []appserver.AppConfig{{Name: "vpn-client"}, {Name: "skychat"}}
	require.True(t, appsContains(apps, "skychat"))
	require.False(t, appsContains(apps, "skysocks"))
	require.False(t, appsContains(nil, "x"))
}

func TestCandidateAddresses(t *testing.T) {
	require.Empty(t, candidateAddresses(&LANDmsgServerInfo{}))

	// Address only.
	require.Equal(t, []string{"1.2.3.4:80"}, candidateAddresses(&LANDmsgServerInfo{Address: "1.2.3.4:80"}))

	// Distinct public address → both.
	got := candidateAddresses(&LANDmsgServerInfo{Address: "1.2.3.4:80", PublicAddress: "5.6.7.8:80"})
	require.Len(t, got, 2)

	// Identical public address → deduped to one.
	got = candidateAddresses(&LANDmsgServerInfo{Address: "1.2.3.4:80", PublicAddress: "1.2.3.4:80"})
	require.Len(t, got, 1)
}

func TestConvertHopInfos(t *testing.T) {
	require.Nil(t, convertHopInfos(nil))

	in := []router.RouteHopInfo{{TpID: "tp1", From: "a", To: "b", TpType: "stcpr"}}
	out := convertHopInfos(in)
	require.Len(t, out, 1)
	require.Equal(t, "tp1", out[0].TpID)
	require.Equal(t, "stcpr", out[0].TpType)
}

func TestDecodeRouteHexErrors(t *testing.T) {
	_, err := decodeRouteHex("")
	require.Error(t, err) // empty rule

	_, err = decodeRouteHex("zzzz") // invalid hex
	require.Error(t, err)
}

func TestStringAndUintDefaults(t *testing.T) {
	require.Equal(t, "x", stringOrDefault("x", "d"))
	require.Equal(t, "d", stringOrDefault("", "d"))
	require.Equal(t, uint(5), uintOrDefault(5, 9))
	require.Equal(t, uint(9), uintOrDefault(0, 9))
}

func TestSecondsAgo(t *testing.T) {
	require.Zero(t, secondsAgo(0))
	require.Zero(t, secondsAgo(-1))
	require.GreaterOrEqual(t, secondsAgo(1), int64(0))
}

func TestStunIsTransientFail(t *testing.T) {
	require.True(t, stunIsTransientFail(stun.NATError))
	require.True(t, stunIsTransientFail(stun.NATUnknown))
	require.True(t, stunIsTransientFail(stun.NATBlocked))
	require.False(t, stunIsTransientFail(stun.NATFull))
}

func TestSanitizeClientLog(t *testing.T) {
	require.Equal(t, "a b c", sanitizeClientLog("a\nb\tc", 100)) // control chars → space, trimmed
	require.Contains(t, sanitizeClientLog("abcdefgh", 4), "truncated")
	require.Equal(t, "ok", sanitizeClientLog("  ok\x00  ", 100)) // null dropped, trimmed
}

func TestShufflePubKeys(t *testing.T) {
	keys := make([]cipher.PubKey, 5)
	for i := range keys {
		keys[i], _ = cipher.GenerateKeyPair()
	}
	out := shufflePubKeys(keys)
	require.Len(t, out, 5)
}

func TestTotalBytes(t *testing.T) {
	require.Zero(t, totalBytes(nil))
	r := &stats.TransportRecord{
		Current: &stats.LiveSnapshot{SentBytes: 10, RecvBytes: 5},
		Daily:   []stats.DailyRollup{{SentBytes: 1, RecvBytes: 2}},
	}
	require.EqualValues(t, 18, totalBytes(r))
}

func TestSummarizeLatencies(t *testing.T) {
	empty := summarizeLatencies("pk", nil)
	require.Equal(t, "pk", empty.Target)
	require.Empty(t, empty.LatenciesMs)

	resp := summarizeLatencies("pk", []time.Duration{10 * time.Millisecond, 30 * time.Millisecond, 0})
	require.Equal(t, "pk", resp.Target)
	require.Len(t, resp.LatenciesMs, 3)
	require.EqualValues(t, 2, resp.SuccessCount) // the 0 (failed) isn't counted
}

func TestStrSliceFromQuery(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/?k=a&k=b", nil)
	require.Equal(t, []string{"a", "b"}, strSliceFromQuery(r, "k", nil))
	require.Equal(t, []string{"def"}, strSliceFromQuery(r, "missing", []string{"def"}))
}

func TestUUIDFromParam(t *testing.T) {
	// No chi route context → empty param → parse error (covers the helper).
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	_, err := uuidFromParam(r, "id")
	require.Error(t, err)
}

func TestVerifyCSRFToken(t *testing.T) {
	require.ErrorIs(t, verifyCSRFToken("no-dot"), ErrCSRFInvalid)
	require.Error(t, verifyCSRFToken("!!!.sig")) // bad base64 in signing string

	// A freshly-minted token round-trips and verifies.
	tok, err := newCSRFToken()
	require.NoError(t, err)
	require.NoError(t, verifyCSRFToken(tok))

	// Tampering with the signature → invalid.
	parts := strings.SplitN(tok, ".", 2)
	require.ErrorIs(t, verifyCSRFToken(parts[0]+".wrongsig"), ErrCSRFInvalid)

	// A correctly-signed but non-JSON payload → unmarshal error (not ErrCSRFInvalid).
	signing := []byte("not-json")
	part0 := base64.RawURLEncoding.EncodeToString(signing)
	h := hmac.New(sha256.New, csrfSecretKey)
	_, _ = h.Write(signing)
	part1 := base64.RawURLEncoding.EncodeToString(h.Sum(nil))
	require.Error(t, verifyCSRFToken(part0+"."+part1))
}
