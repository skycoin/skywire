package skysocksc

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
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
	body, _ := io.ReadAll(rec.Body)
	hs := sha256.Sum256([]byte("hello"))
	require.Contains(t, string(body), `"bytes":5`)
	require.Contains(t, string(body), hex.EncodeToString(hs[:]))

	rec = httptest.NewRecorder()
	loadtestFixed(rec, httptest.NewRequest(http.MethodGet, "/?bytes=x", nil), "x")
	require.Equal(t, http.StatusBadRequest, rec.Code)
}
