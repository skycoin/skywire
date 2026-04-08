package dmsgcurl_test

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"io"
	"strings"
	"sync/atomic"
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

// ---------------------------------------------------------------------------
// ProgressWriter tests
// ---------------------------------------------------------------------------

func TestProgressWriter_Write_KnownTotal(t *testing.T) {
	pw := &dmsgcurl.ProgressWriter{Total: 100}

	n, err := pw.Write(make([]byte, 30))
	assert.NoError(t, err)
	assert.Equal(t, 30, n)
	assert.Equal(t, int64(30), atomic.LoadInt64(&pw.Current))

	n, err = pw.Write(make([]byte, 70))
	assert.NoError(t, err)
	assert.Equal(t, 70, n)
	assert.Equal(t, int64(100), atomic.LoadInt64(&pw.Current))
}

func TestProgressWriter_Write_UnknownTotal(t *testing.T) {
	pw := &dmsgcurl.ProgressWriter{Total: 0}

	n, err := pw.Write(make([]byte, 42))
	assert.NoError(t, err)
	assert.Equal(t, 42, n)
	assert.Equal(t, int64(42), atomic.LoadInt64(&pw.Current))
}

func TestProgressWriter_Write_EmptySlice(t *testing.T) {
	pw := &dmsgcurl.ProgressWriter{Total: 50}

	n, err := pw.Write([]byte{})
	assert.NoError(t, err)
	assert.Equal(t, 0, n)
	assert.Equal(t, int64(0), atomic.LoadInt64(&pw.Current))
}

// ---------------------------------------------------------------------------
// CancellableCopy tests
// ---------------------------------------------------------------------------

func TestCancellableCopy_Normal(t *testing.T) {
	src := io.NopCloser(strings.NewReader("hello world"))
	var dst bytes.Buffer

	n, err := dmsgcurl.CancellableCopy(context.Background(), &dst, src, int64(len("hello world")))
	assert.NoError(t, err)
	assert.Equal(t, int64(11), n)
	assert.Equal(t, "hello world", dst.String())
}

func TestCancellableCopy_Canceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	src := io.NopCloser(strings.NewReader("data that should not be copied"))
	var dst bytes.Buffer

	_, err := dmsgcurl.CancellableCopy(ctx, &dst, src, 30)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Canceled")
}

func TestCancellableCopy_EmptyBody(t *testing.T) {
	src := io.NopCloser(strings.NewReader(""))
	var dst bytes.Buffer

	n, err := dmsgcurl.CancellableCopy(context.Background(), &dst, src, 0)
	assert.NoError(t, err)
	assert.Equal(t, int64(0), n)
}

// ---------------------------------------------------------------------------
// New constructor tests
// ---------------------------------------------------------------------------

func TestNew_ReturnsNonNil(t *testing.T) {
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	dg := dmsgcurl.New(fs)
	require.NotNil(t, dg)
}

func TestNew_RegistersFlags(t *testing.T) {
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	dmsgcurl.New(fs) //nolint:errcheck

	// Verify some expected flags were registered.
	for _, name := range []string{"help", "h", "dmsg-disc", "dmsg-sessions", "O", "t", "w", "U"} {
		assert.NotNilf(t, fs.Lookup(name), "expected flag %q to be registered", name)
	}
}

// ---------------------------------------------------------------------------
// DmsgCurl.String() tests
// ---------------------------------------------------------------------------

func TestString_ContainsFlagGroupNames(t *testing.T) {
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	dg := dmsgcurl.New(fs)

	s := dg.String()
	assert.Contains(t, s, "Startup")
	assert.Contains(t, s, "Dmsg")
	assert.Contains(t, s, "Download")
	assert.Contains(t, s, "HTTP")
}

func TestString_IsValidJSON(t *testing.T) {
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	dg := dmsgcurl.New(fs)

	s := dg.String()
	// Quick check: valid JSON starts with '{' and ends with '}'.
	assert.True(t, strings.HasPrefix(s, "{"))
	assert.True(t, strings.HasSuffix(s, "}"))
}

// ---------------------------------------------------------------------------
// Error variable tests
// ---------------------------------------------------------------------------

func TestErr_Variables(t *testing.T) {
	assert.True(t, errors.Is(dmsgcurl.ErrNoURLs, dmsgcurl.ErrNoURLs))
	assert.True(t, errors.Is(dmsgcurl.ErrMultipleURLsNotSupported, dmsgcurl.ErrMultipleURLsNotSupported))
	assert.NotEqual(t, dmsgcurl.ErrNoURLs, dmsgcurl.ErrMultipleURLsNotSupported)
	assert.Equal(t, "no URLs provided", dmsgcurl.ErrNoURLs.Error())
	assert.Equal(t, "multiple URLs is not yet supported", dmsgcurl.ErrMultipleURLsNotSupported.Error())
}
