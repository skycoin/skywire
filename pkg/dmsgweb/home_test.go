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

	// "other" is a deployment-service alias with NO manifest entry, so it stays
	// in the flat "Deployment services" index and keeps its plain /health link.
	otherPK, _ := cipher.GenerateKeyPair()
	aliases := map[string]cipher.PubKey{
		"skywire": selfPK,
		"tpd":     tpdPK,
		"other":   otherPK,
		"dmsg0":   dmsg0PK,
		"rsn":     rsnPK,
	}
	page := string(renderHomePage(aliases, ".dmsg", selfPK))

	// Grouped under the right headings. "tpd" has a manifest, so it renders as a
	// "Service API endpoints" section; "other" has none, so it falls under the
	// flat "Deployment services" index.
	assert.Contains(t, page, "This visor")
	assert.Contains(t, page, "Deployment services")
	assert.Contains(t, page, "Service API endpoints")
	assert.Contains(t, page, "Setup nodes")
	assert.Contains(t, page, "dmsg servers")

	// /health renders once for tpd — from the manifest section (not also from
	// the flat index, which now omits manifested services).
	assert.Contains(t, page, `href="http://tpd.dmsg/health"`)
	assert.Equal(t, 1, strings.Count(page, `href="http://tpd.dmsg/health"`), "/health must render exactly once for tpd")
	// The visor's own landing page serves "/"; non-manifest services keep their
	// plain /health link.
	assert.Contains(t, page, `href="http://skywire.dmsg/"`)
	assert.Contains(t, page, `href="http://other.dmsg/health"`)
	assert.Contains(t, page, `href="http://dmsg0.dmsg/health"`)

	// Full 66-char PKs, never truncated.
	assert.Contains(t, page, tpdPK.Hex())
	assert.Contains(t, page, selfPK.Hex())

	// The self alias is in the "This visor" group, ahead of the service group.
	assert.Less(t, strings.Index(page, "This visor"), strings.Index(page, "Deployment services"))
}

func TestRenderHomePageServiceEndpoints(t *testing.T) {
	tpdPK, _ := cipher.GenerateKeyPair()
	dmsgdPK, _ := cipher.GenerateKeyPair()

	// Only tpd + dmsgd configured → only those services get an endpoint
	// directory; ar/rf/sd/ut/reward (no alias) are skipped.
	aliases := map[string]cipher.PubKey{"tpd": tpdPK, "dmsgd": dmsgdPK}
	page := string(renderHomePage(aliases, ".dmsg", cipher.PubKey{}))

	assert.Contains(t, page, "Service API endpoints")
	// A no-param GET endpoint from the manifest stays a plain link, paired with
	// the service's resolver alias and tunneled through the same proxy.
	assert.Contains(t, page, `href="http://tpd.dmsg/all-transports"`)
	assert.Contains(t, page, `href="http://dmsgd.dmsg/dmsg-discovery/available_servers"`)
	// A service with no configured alias is not rendered.
	assert.NotContains(t, page, "http://ar.dmsg/resolve")
	assert.NotContains(t, page, "http://sd.dmsg/api/services")

	// Path-param endpoints render an input + Open button, NOT a broken link with
	// an URL-encoded "{pk}" placeholder.
	assert.NotContains(t, page, "%7Bpk%7D", "path placeholders must not be URL-encoded into a dead link")
	assert.NotContains(t, page, `href="http://tpd.dmsg/transports/id:{id}"`)
	assert.Contains(t, page, `data-kind="path" data-name="id"`)
	assert.Contains(t, page, `data-path="/transports/id:{id}"`)
	assert.Contains(t, page, `onclick="skyOpen(this)"`)

	// Query-param endpoints surface an input per declared query param.
	assert.Contains(t, page, `data-kind="query" data-name="v"`)
	assert.Contains(t, page, `data-kind="query" data-name="visors"`)
}

func TestRenderHomePagePostEndpoint(t *testing.T) {
	rfPK, _ := cipher.GenerateKeyPair()
	page := string(renderHomePage(map[string]cipher.PubKey{"rf": rfPK}, ".dmsg", cipher.PubKey{}))

	// route-finder POST /routes renders a body textarea + Send button (which
	// fetches through the proxy) instead of a navigating link.
	assert.Contains(t, page, "Service API endpoints")
	assert.Contains(t, page, `onclick="skySend(this)"`)
	assert.Contains(t, page, `textarea data-kind="body"`)
	assert.Contains(t, page, `pre data-kind="resp"`)
	// The body hint / example JSON is surfaced as the textarea placeholder.
	assert.Contains(t, page, "&#34;Edges&#34;")
	// rf /health (a no-param GET) is still a plain link, rendered once.
	assert.Equal(t, 1, strings.Count(page, `href="http://rf.dmsg/health"`))
	// The explorer JS is present.
	assert.Contains(t, page, "function skySend(")
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
