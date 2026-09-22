package loadtest

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestLoadtestFixedIsDeterministicAndCertified pins the finite sink: the same
// request yields the same bytes, exactly N of them, and X-Sha256 names their
// hash; the upload sink reports what it received.
func TestLoadtestFixedIsDeterministicAndCertified(t *testing.T) {
	get := func() ([]byte, string) {
		rec := httptest.NewRecorder()
		serveFixed(rec, httptest.NewRequest(http.MethodGet, "/?bytes=300001", nil), "300001")
		require.Equal(t, http.StatusOK, rec.Code)
		return rec.Body.Bytes(), rec.Header().Get("X-Sha256")
	}
	a, ha := get()
	b, hb := get()
	require.Len(t, a, 300001)
	require.Equal(t, a, b, "the pattern is a function of the request")
	sum := sha256.Sum256(a)
	require.Equal(t, hex.EncodeToString(sum[:]), ha)
	require.Equal(t, ha, hb)
	require.NotEqual(t, strings.Repeat("\x00", 64), string(a[:64]), "not zeros")

	rec := httptest.NewRecorder()
	serveUpload(rec, httptest.NewRequest(http.MethodPost, "/upload", strings.NewReader("hello")))
	require.Equal(t, http.StatusOK, rec.Code)
	body, _ := io.ReadAll(rec.Body) //nolint:errcheck
	hs := sha256.Sum256([]byte("hello"))
	require.Contains(t, string(body), `"bytes":5`)
	require.Contains(t, string(body), hex.EncodeToString(hs[:]))

	rec = httptest.NewRecorder()
	serveFixed(rec, httptest.NewRequest(http.MethodGet, "/?bytes=x", nil), "x")
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

// TestLoadtestFixedServesRanges proves the sink is range-capable the way the
// proxy's transparent range-splitter needs: any byte range is a slice of the
// same deterministic body (including one that is not chunk-aligned), served as
// 206 with Content-Range, X-Sha256 still certifying the whole body; a
// suffix range and an unsatisfiable one behave per RFC 9110.
func TestLoadtestFixedServesRanges(t *testing.T) {
	const n = 700_000 // spans three 256 KiB chunks
	get := func(rng string) (*httptest.ResponseRecorder, []byte) {
		req := httptest.NewRequest(http.MethodGet, "/?bytes=700000", nil)
		if rng != "" {
			req.Header.Set("Range", rng)
		}
		rec := httptest.NewRecorder()
		serveFixed(rec, req, "700000")
		return rec, rec.Body.Bytes()
	}
	full, body := get("")
	if full.Code != http.StatusOK || len(body) != n || full.Header().Get("Accept-Ranges") != "bytes" {
		t.Fatalf("full: code=%d len=%d accept-ranges=%q", full.Code, len(body), full.Header().Get("Accept-Ranges"))
	}
	var joined []byte
	for _, r := range []string{"bytes=0-99999", "bytes=100000-300000", "bytes=300001-"} {
		rec, part := get(r)
		if rec.Code != http.StatusPartialContent {
			t.Fatalf("%s: code=%d, want 206", r, rec.Code)
		}
		if rec.Header().Get("X-Sha256") != full.Header().Get("X-Sha256") {
			t.Fatalf("%s: X-Sha256 must certify the whole body", r)
		}
		if got, want := rec.Header().Get("Content-Length"), strconv.Itoa(len(part)); got != want {
			t.Fatalf("%s: Content-Length=%s, body=%s", r, got, want)
		}
		joined = append(joined, part...)
	}
	if !bytes.Equal(joined, body) {
		t.Fatal("ranges do not reassemble to the full body")
	}
	if rec, part := get("bytes=-5"); rec.Code != http.StatusPartialContent || !bytes.Equal(part, body[n-5:]) || rec.Header().Get("Content-Range") != "bytes 699995-699999/700000" {
		t.Fatalf("suffix range: code=%d content-range=%q", rec.Code, rec.Header().Get("Content-Range"))
	}
	if rec, _ := get("bytes=700000-"); rec.Code != http.StatusRequestedRangeNotSatisfiable || rec.Header().Get("Content-Range") != "bytes */700000" {
		t.Fatalf("unsatisfiable range: code=%d content-range=%q", rec.Code, rec.Header().Get("Content-Range"))
	}
}

// TestLoadtestFixedHashesAnObjectOnce pins the cost fix: the whole-object
// SHA-256 that X-Sha256 carries is a function of N alone, so it is computed once
// per N however many ranged GETs ask for it — a range-split download of a 50 MB
// object used to make the sink hash 600 MB. The ranged bytes must still be the
// whole object's bytes at that offset.
func TestLoadtestFixedHashesAnObjectOnce(t *testing.T) {
	const nStr = "1234567" // five 256 KiB chunks; a size no other test uses
	get := func(rng string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/?bytes="+nStr, nil)
		if rng != "" {
			req.Header.Set("Range", rng)
		}
		rec := httptest.NewRecorder()
		serveFixed(rec, req, nStr)
		return rec
	}

	before := sumCalcs.Load()
	full := get("")
	require.Equal(t, http.StatusOK, full.Code)
	require.Equal(t, before+1, sumCalcs.Load(), "the first request computes the hash")
	body := full.Body.Bytes()
	require.Len(t, body, 1234567)

	after := sumCalcs.Load()
	for _, r := range []string{"bytes=0-299999", "bytes=300000-1234566"} {
		rec := get(r)
		require.Equal(t, http.StatusPartialContent, rec.Code, r)
		require.Equal(t, full.Header().Get("X-Sha256"), rec.Header().Get("X-Sha256"), r)
		s, e, ok := parseByteRange(r, 1234567)
		require.True(t, ok, r)
		require.Equal(t, body[s:e+1], rec.Body.Bytes(), "%s: a range is the whole object's bytes at that offset", r)
	}
	require.Equal(t, after, sumCalcs.Load(), "a ranged GET must not re-hash the whole object")
}

// TestLoadtestSumIsComputedOnceUnderConcurrency covers the shape the bench
// actually produces: the chunks of one range-split download arrive together, so
// a plain map cache would let every one of them start its own hash. They share
// the single computation instead.
func TestLoadtestSumIsComputedOnceUnderConcurrency(t *testing.T) {
	const n = 7_654_321 // a size no other test uses
	before := sumCalcs.Load()
	var wg sync.WaitGroup
	sums := make([]string, 16)
	for i := range sums {
		wg.Add(1)
		go func(i int) { defer wg.Done(); sums[i] = objectSum(n) }(i)
	}
	wg.Wait()
	require.Equal(t, before+1, sumCalcs.Load(), "16 concurrent askers, one computation")
	for _, s := range sums {
		require.Equal(t, sums[0], s)
		require.Len(t, s, 64)
	}
}
