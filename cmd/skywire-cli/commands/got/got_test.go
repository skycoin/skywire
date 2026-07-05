// Package cligot got_test.go: unit tests for the got CLI's URL parsing,
// header/body helpers, byte formatting, the got client factory, and command
// wiring. The request/download/head paths require a running visor RPC or
// network and are not exercised here.
package cligot

import (
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/got"
)

// ---- isSkywireURL ----------------------------------------------------------

func TestIsSkywireURL(t *testing.T) {
	require.True(t, isSkywireURL("skynet://abc/path"))
	require.True(t, isSkywireURL("dmsg://abc/path"))
	require.True(t, isSkywireURL("  skynet://abc")) // leading space trimmed
	require.False(t, isSkywireURL("http://example.com"))
	require.False(t, isSkywireURL("https://example.com"))
	require.False(t, isSkywireURL(""))
}

// ---- parseSkywireURL -------------------------------------------------------

func TestParseSkywireURL(t *testing.T) {
	pk, _ := cipher.GenerateKeyPair()
	hex := pk.Hex()

	t.Run("skynet with port and path", func(t *testing.T) {
		tgt, err := parseSkywireURL("skynet://" + hex + ":8080/foo/bar")
		require.NoError(t, err)
		require.Equal(t, "skynet", tgt.scheme)
		require.Equal(t, pk, tgt.pk)
		require.Equal(t, uint16(8080), tgt.port)
		require.Equal(t, "/foo/bar", tgt.path)
	})

	t.Run("dmsg defaults port 80 and path /", func(t *testing.T) {
		tgt, err := parseSkywireURL("dmsg://" + hex)
		require.NoError(t, err)
		require.Equal(t, "dmsg", tgt.scheme)
		require.Equal(t, uint16(80), tgt.port)
		require.Equal(t, "/", tgt.path)
	})

	t.Run("strips .skynet host suffix", func(t *testing.T) {
		tgt, err := parseSkywireURL("skynet://" + hex + ".skynet/health")
		require.NoError(t, err)
		require.Equal(t, pk, tgt.pk)
		require.Equal(t, "/health", tgt.path)
	})

	t.Run("non-skywire scheme", func(t *testing.T) {
		_, err := parseSkywireURL("http://example.com")
		require.Error(t, err)
	})

	t.Run("invalid port", func(t *testing.T) {
		_, err := parseSkywireURL("skynet://" + hex + ":notaport/")
		require.Error(t, err)
	})

	t.Run("invalid public key", func(t *testing.T) {
		_, err := parseSkywireURL("skynet://not-a-pk:80/")
		require.Error(t, err)
	})
}

// ---- buildHeaderMap --------------------------------------------------------

func TestBuildHeaderMap(t *testing.T) {
	out, err := buildHeaderMap(nil)
	require.NoError(t, err)
	require.Nil(t, out)

	out, err = buildHeaderMap([]string{"Content-Type: application/json", "X-Token:  abc  "})
	require.NoError(t, err)
	require.Equal(t, "application/json", out["Content-Type"])
	require.Equal(t, "abc", out["X-Token"]) // value trimmed

	_, err = buildHeaderMap([]string{"no-colon-here"})
	require.Error(t, err)
}

// ---- humanBytes ------------------------------------------------------------

func TestHumanBytes(t *testing.T) {
	cases := []struct {
		in   uint64
		want string
	}{
		{0, "0 B"},
		{512, "512 B"},
		{1023, "1023 B"},
		{1024, "1.0 KB"},
		{1536, "1.5 KB"},
		{1024 * 1024, "1.0 MB"},
		{1024 * 1024 * 1024, "1.0 GB"},
	}
	for _, tc := range cases {
		require.Equal(t, tc.want, humanBytes(tc.in), "humanBytes(%d)", tc.in)
	}
}

// ---- getBodyReader ---------------------------------------------------------

func TestGetBodyReader(t *testing.T) {
	t.Run("inline string", func(t *testing.T) {
		rc, err := getBodyReader("hello body")
		require.NoError(t, err)
		defer rc.Close() //nolint:errcheck
		b, err := io.ReadAll(rc)
		require.NoError(t, err)
		require.Equal(t, "hello body", string(b))
	})

	t.Run("@file", func(t *testing.T) {
		dir := t.TempDir()
		f := filepath.Join(dir, "payload.json")
		require.NoError(t, os.WriteFile(f, []byte(`{"k":"v"}`), 0o600))

		rc, err := getBodyReader("@" + f)
		require.NoError(t, err)
		defer rc.Close() //nolint:errcheck
		b, err := io.ReadAll(rc)
		require.NoError(t, err)
		require.Equal(t, `{"k":"v"}`, string(b))
	})

	t.Run("@missing file", func(t *testing.T) {
		_, err := getBodyReader("@/nonexistent/payload.json")
		require.Error(t, err)
	})
}

// ---- newGot ----------------------------------------------------------------

func TestNewGot(t *testing.T) {
	origUA, origProxy := userAgent, proxyAddr
	defer func() { userAgent, proxyAddr = origUA, origProxy }()

	t.Run("default (no proxy)", func(t *testing.T) {
		userAgent, proxyAddr = "", ""
		g, err := newGot()
		require.NoError(t, err)
		require.NotNil(t, g)
	})

	t.Run("custom user agent", func(t *testing.T) {
		userAgent, proxyAddr = "MyAgent/1.0", ""
		g, err := newGot()
		require.NoError(t, err)
		require.NotNil(t, g)
		require.Equal(t, "MyAgent/1.0", got.UserAgent)
	})

	t.Run("with proxy", func(t *testing.T) {
		userAgent, proxyAddr = "", "127.0.0.1:1080"
		g, err := newGot()
		require.NoError(t, err) // SOCKS5 dialer is built lazily, no dial here
		require.NotNil(t, g)
	})
}

// ---- progressFunc ----------------------------------------------------------

func TestProgressFunc(t *testing.T) {
	// Unknown total size exercises the no-percentage branch; a zero-value
	// Download reports TotalSize() == 0. Just confirm it runs without panic.
	pf := progressFunc()
	require.NotNil(t, pf)
	require.NotPanics(t, func() { pf(&got.Download{}) })
}

// ---- command wiring --------------------------------------------------------

func TestCommandWiring(t *testing.T) {
	names := map[string]bool{}
	for _, c := range RootCmd.Commands() {
		names[c.Name()] = true
	}
	require.True(t, names["dl"], "dl subcommand should be registered")
	require.True(t, names["req"], "req subcommand should be registered")
	require.True(t, names["head"], "head subcommand should be registered")

	// A representative flag on each subcommand.
	require.NotNil(t, dlCmd.Flags().Lookup("output"))
	require.NotNil(t, dlCmd.Flags().Lookup("concurrency"))
	require.NotNil(t, reqCmd.Flags().Lookup("data"))
	require.NotNil(t, reqCmd.Flags().Lookup("verbose"))
	require.NotNil(t, headCmd.Flags().Lookup("header"))
}
