// Package commands cmd/dmsg/conf/commands/commands_test.go
//
// These tests cover the package's pure, deterministic logic: parsing a dmsg URL
// to a public key, formatting discovery entries into the services-config
// dmsg_servers block, the regex splice that rewrites that block, and the
// verify-keys command's secret-key validation. They do NOT cover the network
// orchestration (fetchAllServersOverDmsg / the pull RunE), which bootstraps a
// real dmsg client, nor the cobra wiring or stdout-only commands.
package commands

import (
	"io"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/disc"
)

// captureStdout runs fn with os.Stdout redirected and returns what it wrote.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	orig := os.Stdout
	r, w, err := os.Pipe()
	require.NoError(t, err)
	os.Stdout = w
	defer func() { os.Stdout = orig }()

	fn()
	require.NoError(t, w.Close())
	out, err := io.ReadAll(r)
	require.NoError(t, err)
	return string(out)
}

// TestPkFromDmsgURL covers the dmsg:// URL → PubKey parser across its forms.
func TestPkFromDmsgURL(t *testing.T) {
	realPK, _ := cipher.GenerateKeyPair()
	hexPK := realPK.Hex()

	t.Run("bare pk", func(t *testing.T) {
		got, err := pkFromDmsgURL(hexPK)
		require.NoError(t, err)
		assert.Equal(t, hexPK, got.Hex())
	})

	t.Run("dmsg scheme prefix", func(t *testing.T) {
		got, err := pkFromDmsgURL("dmsg://" + hexPK)
		require.NoError(t, err)
		assert.Equal(t, hexPK, got.Hex())
	})

	t.Run("with port", func(t *testing.T) {
		got, err := pkFromDmsgURL("dmsg://" + hexPK + ":80")
		require.NoError(t, err)
		assert.Equal(t, hexPK, got.Hex())
	})

	t.Run("with path", func(t *testing.T) {
		got, err := pkFromDmsgURL("dmsg://" + hexPK + "/some/path")
		require.NoError(t, err)
		assert.Equal(t, hexPK, got.Hex())
	})

	t.Run("invalid", func(t *testing.T) {
		_, err := pkFromDmsgURL("dmsg://not-a-key")
		assert.Error(t, err)
	})
}

// entry builds a *disc.Entry with a static key and server address.
func entry(t *testing.T, addr string) *disc.Entry {
	t.Helper()
	pk, _ := cipher.GenerateKeyPair()
	return &disc.Entry{Static: pk, Server: &disc.Server{Address: addr}}
}

// TestFormatDmsgServers verifies the rendered block opens with the dmsg_servers
// key, closes at the 4-space indent the splice regex expects, and contains each
// entry's static key and address.
func TestFormatDmsgServers(t *testing.T) {
	e1 := entry(t, "1.1.1.1:80")
	e2 := entry(t, "2.2.2.2:80")

	out := formatDmsgServers([]*disc.Entry{e1, e2})

	assert.True(t, strings.HasPrefix(out, `"dmsg_servers": [`))
	assert.True(t, strings.HasSuffix(out, "\n    ]"), "must close at the 4-space deployment indent")
	assert.Contains(t, out, e1.Static.Hex())
	assert.Contains(t, out, `"address": "1.1.1.1:80"`)
	assert.Contains(t, out, `"address": "2.2.2.2:80"`)
	// Two entries → exactly one separating comma between objects.
	assert.Equal(t, 1, strings.Count(out, "},"))
}

// TestFormatDmsgServersSingle verifies a single entry renders without a trailing
// comma.
func TestFormatDmsgServersSingle(t *testing.T) {
	out := formatDmsgServers([]*disc.Entry{entry(t, "3.3.3.3:80")})
	assert.Equal(t, 0, strings.Count(out, "},"))
}

const sampleConfig = `{
  "prod": {
    "dmsg_servers": [
      {
        "static": "deadbeef",
        "server": {
          "address": "9.9.9.9:80"
        }
      }
    ]
  }
}`

// TestSpliceDmsgServers verifies the regex finds and replaces a dmsg_servers
// block, reporting the replacement count.
func TestSpliceDmsgServers(t *testing.T) {
	e := entry(t, "5.5.5.5:80")
	updated, n := spliceDmsgServers([]byte(sampleConfig), []*disc.Entry{e})

	assert.Equal(t, 1, n)
	s := string(updated)
	assert.Contains(t, s, e.Static.Hex())
	assert.Contains(t, s, `"address": "5.5.5.5:80"`)
	// Old contents are gone.
	assert.NotContains(t, s, "9.9.9.9:80")
	assert.NotContains(t, s, `"static": "deadbeef"`)
	// Surrounding structure is preserved.
	assert.True(t, strings.HasPrefix(s, "{\n  \"prod\": {"))
}

// TestSpliceDmsgServersNoMatch verifies content without a dmsg_servers block is
// returned unchanged with a zero count (the RunE treats this as an error).
func TestSpliceDmsgServersNoMatch(t *testing.T) {
	in := `{"prod": {"something_else": []}}`
	updated, n := spliceDmsgServers([]byte(in), []*disc.Entry{entry(t, "x:1")})
	assert.Equal(t, 0, n)
	assert.Equal(t, in, string(updated))
}

// TestSpliceDmsgServersMultiple verifies every dmsg_servers block is replaced
// (the prod/test deployment sections each have one).
func TestSpliceDmsgServersMultiple(t *testing.T) {
	doubled := sampleConfig + "\n" + sampleConfig
	_, n := spliceDmsgServers([]byte(doubled), []*disc.Entry{entry(t, "6.6.6.6:80")})
	assert.Equal(t, 2, n)
}

// TestVerifyKeysCmd covers the verify-keys command's RunE: invalid input is
// rejected, a well-formed secret key is accepted.
func TestVerifyKeysCmd(t *testing.T) {
	t.Run("invalid secret key", func(t *testing.T) {
		err := verifyKeysCmd.RunE(verifyKeysCmd, []string{"not-a-secret-key"})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "invalid secret key")
	})

	t.Run("valid secret key", func(t *testing.T) {
		_, sk := cipher.GenerateKeyPair()
		assert.NoError(t, verifyKeysCmd.RunE(verifyKeysCmd, []string{sk.Hex()}))
	})
}

// TestGenKeysCmd verifies the gen-keys command prints a parseable public and
// secret key pair on two lines.
func TestGenKeysCmd(t *testing.T) {
	out := captureStdout(t, func() { genKeysCmd.Run(genKeysCmd, nil) })

	lines := strings.Fields(strings.TrimSpace(out))
	require.Len(t, lines, 2, "expected a pk line and an sk line")

	var pk cipher.PubKey
	assert.NoError(t, pk.Set(lines[0]), "first line should be a valid public key")
	var sk cipher.SecKey
	assert.NoError(t, sk.Set(lines[1]), "second line should be a valid secret key")

	// The printed pk must be the one derived from the printed sk.
	derived, err := sk.PubKey()
	require.NoError(t, err)
	assert.Equal(t, derived.Hex(), pk.Hex())
}
