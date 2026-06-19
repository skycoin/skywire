// Package rewards pkg/rewards/server_test.go
package rewards

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
)

func init() { gin.SetMode(gin.TestMode) }

// ginCtx builds a gin context whose request carries the given RemoteAddr,
// mimicking what the DMSG/skynet transport sets (the peer's PK as host).
func ginCtx(remoteAddr string) (*gin.Context, *httptest.ResponseRecorder) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = remoteAddr
	c.Request = req
	return c, w
}

// runAuth runs WhitelistAuth as the only middleware in front of a 200 handler
// and returns the resulting status code for the given RemoteAddr.
func runAuth(wl []cipher.PubKey, remoteAddr string) int {
	r := gin.New()
	r.Use(WhitelistAuth(wl))
	r.GET("/", func(c *gin.Context) { c.Status(http.StatusOK) })

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = remoteAddr
	r.ServeHTTP(w, req)
	return w.Code
}

func TestWhitelistAuth(t *testing.T) {
	pk, _ := cipher.GenerateKeyPair()
	other, _ := cipher.GenerateKeyPair()

	t.Run("empty whitelist allows all", func(t *testing.T) {
		assert.Equal(t, http.StatusOK, runAuth(nil, "127.0.0.1:1234"))
	})

	t.Run("whitelisted PK passes", func(t *testing.T) {
		assert.Equal(t, http.StatusOK, runAuth([]cipher.PubKey{pk}, pk.Hex()+":80"))
	})

	t.Run("non-whitelisted PK is rejected", func(t *testing.T) {
		assert.Equal(t, http.StatusUnauthorized, runAuth([]cipher.PubKey{pk}, other.Hex()+":80"))
	})

	t.Run("null PK is rejected", func(t *testing.T) {
		assert.Equal(t, http.StatusUnauthorized, runAuth([]cipher.PubKey{pk}, "127.0.0.1:1234"))
	})
}

func TestIsWhitelisted(t *testing.T) {
	pk, _ := cipher.GenerateKeyPair()
	other, _ := cipher.GenerateKeyPair()

	t.Run("empty whitelist is always allowed", func(t *testing.T) {
		c, _ := ginCtx("127.0.0.1:1234")
		assert.True(t, IsWhitelisted(c, nil))
	})

	t.Run("whitelisted PK", func(t *testing.T) {
		c, _ := ginCtx(pk.Hex())
		assert.True(t, IsWhitelisted(c, []cipher.PubKey{pk}))
	})

	t.Run("non-whitelisted PK", func(t *testing.T) {
		c, _ := ginCtx(other.Hex())
		assert.False(t, IsWhitelisted(c, []cipher.PubKey{pk}))
	})

	t.Run("null PK is not whitelisted", func(t *testing.T) {
		c, _ := ginCtx("not-a-pk")
		assert.False(t, IsWhitelisted(c, []cipher.PubKey{pk}))
	})
}

func TestExtractPK(t *testing.T) {
	pk, _ := cipher.GenerateKeyPair()

	t.Run("PK with port", func(t *testing.T) {
		c, _ := ginCtx(pk.Hex() + ":8080")
		assert.Equal(t, pk, extractPK(c))
	})

	t.Run("PK without port", func(t *testing.T) {
		c, _ := ginCtx(pk.Hex())
		assert.Equal(t, pk, extractPK(c))
	})

	t.Run("invalid host yields null PK", func(t *testing.T) {
		c, _ := ginCtx("127.0.0.1:1234")
		got := extractPK(c)
		assert.True(t, got.Null())
	})
}

// TestExtractPK_RoundTrip is a sanity check that a generated PK's hex form is
// what extractPK parses back, guarding against any encoding drift.
func TestExtractPK_RoundTrip(t *testing.T) {
	pk, _ := cipher.GenerateKeyPair()
	c, _ := ginCtx(pk.Hex())
	got := extractPK(c)
	require.False(t, got.Null())
	assert.Equal(t, pk.Hex(), got.Hex())
}
