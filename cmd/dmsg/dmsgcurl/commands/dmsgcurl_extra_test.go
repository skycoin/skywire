// Package commands — dmsgcurl_extra_test.go: covers the error-path
// branches of the file/response cleanup helpers that the happy-path
// tests don't reach (Close returning an error, and removal of a partial
// file on failure).
package commands

import (
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// errCloser is an io.ReadCloser whose Close always fails.
type errCloser struct{ *strings.Reader }

func (errCloser) Close() error { return errors.New("close failed") }

func TestCloseResponseBody_CloseError(t *testing.T) {
	resp := &http.Response{Body: errCloser{strings.NewReader("body")}}
	// The Close error is logged at debug and swallowed — must not panic.
	require.NotPanics(t, func() { closeResponseBody(resp) })
}

func TestCloseAndCleanFile_CloseErrorAndRemove(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "partial.bin")
	f, err := os.Create(p)
	require.NoError(t, err)

	// Close it first so closeAndCleanFile's own file.Close() returns an
	// error (double close) — exercising the close-error warn branch. A
	// non-nil download err also drives the partial-file removal branch.
	require.NoError(t, f.Close())
	closeAndCleanFile(f, errors.New("download failed"))

	require.NoFileExists(t, p, "partial file should be removed on error")
}
