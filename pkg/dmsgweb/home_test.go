package dmsgweb

import (
	"bufio"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/skycoin/skywire/pkg/cipher"
)

func TestIsHomeHost(t *testing.T) {
	assert.True(t, isHomeHost("home.dmsg", ".dmsg"))
	assert.True(t, isHomeHost("HOME.dmsg", ".dmsg"))
	assert.True(t, isHomeHost("home.skynet", ".skynet"))
	assert.False(t, isHomeHost("x.home.dmsg", ".dmsg"), "a vhost left of home is not the reserved label")
	assert.False(t, isHomeHost("tpd.dmsg", ".dmsg"))
}

func TestRenderHomePage(t *testing.T) {
	selfPK, _ := cipher.GenerateKeyPair()
	tpdPK, _ := cipher.GenerateKeyPair()
	dmsg0PK, _ := cipher.GenerateKeyPair()
	rsnPK, _ := cipher.GenerateKeyPair()

	aliases := map[string]cipher.PubKey{
		"skywire": selfPK,
		"tpd":     tpdPK,
		"dmsg0":   dmsg0PK,
		"rsn":     rsnPK,
	}
	page := string(renderHomePage(aliases, ".dmsg", selfPK))

	// Grouped under the right headings.
	assert.Contains(t, page, "This visor")
	assert.Contains(t, page, "Deployment services")
	assert.Contains(t, page, "Setup nodes")
	assert.Contains(t, page, "dmsg servers")

	// Service links target /health (services don't serve "/"); the visor's own
	// landing page does, so the self link stays at "/".
	assert.Contains(t, page, `href="http://tpd.dmsg/health"`)
	assert.Contains(t, page, `href="http://skywire.dmsg/"`)
	assert.Contains(t, page, `href="http://dmsg0.dmsg/health"`)

	// Full 66-char PKs, never truncated.
	assert.Contains(t, page, tpdPK.Hex())
	assert.Contains(t, page, selfPK.Hex())

	// The self alias is in the "This visor" group, ahead of the service group.
	assert.Less(t, strings.Index(page, "This visor"), strings.Index(page, "Deployment services"))
}

func TestRenderHomePageNatSort(t *testing.T) {
	pk, _ := cipher.GenerateKeyPair()
	aliases := map[string]cipher.PubKey{"dmsg0": pk, "dmsg2": pk, "dmsg10": pk}
	page := string(renderHomePage(aliases, ".dmsg", cipher.PubKey{}))
	i0, i2, i10 := strings.Index(page, "dmsg0.dmsg"), strings.Index(page, "dmsg2.dmsg"), strings.Index(page, "dmsg10.dmsg")
	assert.True(t, i0 < i2 && i2 < i10, "dmsg2 must sort before dmsg10 (numeric, not lexical)")
}

func TestRenderHomePageEmpty(t *testing.T) {
	page := string(renderHomePage(nil, ".dmsg", cipher.PubKey{}))
	assert.Contains(t, page, "No aliases configured")
}

// TestServeHomeInProcess drives the in-memory pipe with a real HTTP request and
// asserts a well-formed HTML response comes back — the path go-socks5 exercises.
func TestServeHomeInProcess(t *testing.T) {
	tpdPK, _ := cipher.GenerateKeyPair()
	conn := serveHomeInProcess(map[string]cipher.PubKey{"tpd": tpdPK}, ".dmsg", cipher.PubKey{})
	defer conn.Close() //nolint:errcheck

	req, err := http.NewRequest(http.MethodGet, "http://home.dmsg/", nil)
	assert.NoError(t, err)
	assert.NoError(t, req.Write(conn))

	resp, err := http.ReadResponse(bufio.NewReader(conn), req)
	assert.NoError(t, err)
	defer resp.Body.Close() //nolint:errcheck
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "text/html; charset=utf-8", resp.Header.Get("Content-Type"))
	body, err := io.ReadAll(resp.Body)
	assert.NoError(t, err)
	assert.Contains(t, string(body), `href="http://tpd.dmsg/health"`)
}
