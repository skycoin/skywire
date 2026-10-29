package dmsgcurl_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/dmsg/dmsgcurl"
)

// A valid 66-char hex public key for testing.
const testPK = "024ec47420176680816e0406250e7156465e4531f5b26057c9f6297bb0303558c7"

// ---------------------------------------------------------------------------
// URL.Fill tests
// ---------------------------------------------------------------------------

func TestURL_Fill_Valid(t *testing.T) {
	tests := []struct {
		name string
		url  string
	}{
		{"with port and path", "dmsg://" + testPK + ":80/some/path"},
		{"without port", "dmsg://" + testPK + "/file.txt"},
		{"http scheme", "http://" + testPK + ":8080/index.html"},
		{"root path", "dmsg://" + testPK + ":443/"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var u dmsgcurl.URL
			err := u.Fill(tc.url)
			assert.NoError(t, err)
			assert.NotEmpty(t, u.URL.Host)
		})
	}
}

func TestURL_Fill_MissingScheme(t *testing.T) {
	// URL without a scheme - url.Parse may return its own error or Fill detects missing scheme.
	var u dmsgcurl.URL
	err := u.Fill(testPK + ":80/path")
	require.Error(t, err)

	// Also test a path-only URL that url.Parse accepts but has no scheme.
	var u2 dmsgcurl.URL
	err2 := u2.Fill("//example/path")
	require.Error(t, err2)
	assert.Contains(t, err2.Error(), "missing a scheme")
}

func TestURL_Fill_MissingHost(t *testing.T) {
	var u dmsgcurl.URL
	err := u.Fill("dmsg:///path/only")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "missing a host")
}

func TestURL_Fill_InvalidHost(t *testing.T) {
	// Host is not a valid public key.
	var u dmsgcurl.URL
	err := u.Fill("dmsg://not-a-pubkey:80/path")
	require.Error(t, err)
}

func TestURL_Fill_EmptyString(t *testing.T) {
	var u dmsgcurl.URL
	err := u.Fill("")
	require.Error(t, err)
}
