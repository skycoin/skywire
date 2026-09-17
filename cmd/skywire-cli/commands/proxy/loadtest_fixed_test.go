package skysocksc

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestLoadtestFixedIsDeterministicAndCertified pins the finite sink: the same
// request yields the same bytes, exactly N of them, and X-Sha256 names their
// hash; the upload sink reports what it received.
func TestLoadtestFixedIsDeterministicAndCertified(t *testing.T) {
	get := func() ([]byte, string) {
		rec := httptest.NewRecorder()
		loadtestFixed(rec, httptest.NewRequest(http.MethodGet, "/?bytes=300001", nil), "300001")
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
	loadtestUpload(rec, httptest.NewRequest(http.MethodPost, "/upload", strings.NewReader("hello")))
	require.Equal(t, http.StatusOK, rec.Code)
	body, _ := io.ReadAll(rec.Body) //nolint:errcheck
	hs := sha256.Sum256([]byte("hello"))
	require.Contains(t, string(body), `"bytes":5`)
	require.Contains(t, string(body), hex.EncodeToString(hs[:]))

	rec = httptest.NewRecorder()
	loadtestFixed(rec, httptest.NewRequest(http.MethodGet, "/?bytes=x", nil), "x")
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
		loadtestFixed(rec, req, "700000")
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
