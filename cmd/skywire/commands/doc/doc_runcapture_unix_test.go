//go:build !windows

// Package doc cmd/skywire/commands/doc/doc_runcapture_unix_test.go: tests for
// the runCapture exec wrapper. These are Unix-only: the stub "skywire" binary
// is a /bin/sh script (shebang + mode 0755), which Windows cannot exec — it has
// no portable shell-script equivalent, so the runCapture path is exercised on
// Unix only.
package doc

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// stubSkywire writes an executable shell script named "skywire" so runCapture
// (which uses os.Args[0] when it ends in "skywire") invokes it instead of a
// real binary. body is the script after the shebang.
func stubSkywire(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "skywire")
	require.NoError(t, os.WriteFile(p, []byte("#!/bin/sh\n"+body), 0o755)) //nolint:gosec
	return p
}

func TestRunCapture_Success(t *testing.T) {
	saveGlobals(t)
	captureTimeout = 5 * time.Second
	os.Args = []string{stubSkywire(t, "echo line1\necho line2\necho line3\n")}

	out, errMsg := runCapture(captureSpec{argv: []string{"x"}, maxLines: 2})
	require.Equal(t, "", errMsg)
	require.Contains(t, out, "line1")
	require.Contains(t, out, "... (1 more lines)")
}

func TestRunCapture_Failure(t *testing.T) {
	saveGlobals(t)
	captureTimeout = 5 * time.Second
	os.Args = []string{stubSkywire(t, "echo problem >&2\nexit 1\n")}

	out, errMsg := runCapture(captureSpec{argv: []string{"x"}})
	require.Equal(t, "", out)
	require.Equal(t, "problem", errMsg) // first stderr line is the reason
}

func TestRunCapture_Timeout(t *testing.T) {
	saveGlobals(t)
	captureTimeout = 50 * time.Millisecond
	os.Args = []string{stubSkywire(t, "sleep 5\n")}

	out, errMsg := runCapture(captureSpec{argv: []string{"x"}})
	require.Equal(t, "", out)
	require.Contains(t, errMsg, "timed out")
}
