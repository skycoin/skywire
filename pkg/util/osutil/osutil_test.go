//go:build !windows

// Package osutil pkg/util/osutil/osutil_test.go: unit tests for the process and
// socket-file helpers (unix variants).
package osutil

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestErrorWithStderr verifies the wrapper renders the underlying error and the
// captured stderr together.
func TestErrorWithStderr(t *testing.T) {
	err := NewErrorWithStderr(errors.New("boom"), []byte("bad stuff"))
	assert.Equal(t, "boom: bad stuff", err.Error())
}

// TestUnlinkSocketFiles covers the success path, the missing-file path (which is
// ignored), and the error path (unlinking a directory).
func TestUnlinkSocketFiles(t *testing.T) {
	t.Run("removes existing files", func(t *testing.T) {
		dir := t.TempDir()
		f1 := filepath.Join(dir, "a.sock")
		f2 := filepath.Join(dir, "b.sock")
		require.NoError(t, os.WriteFile(f1, nil, 0600))
		require.NoError(t, os.WriteFile(f2, nil, 0600))

		require.NoError(t, UnlinkSocketFiles(f1, f2))
		assert.NoFileExists(t, f1)
		assert.NoFileExists(t, f2)
	})

	t.Run("missing file is ignored", func(t *testing.T) {
		missing := filepath.Join(t.TempDir(), "nope.sock")
		assert.NoError(t, UnlinkSocketFiles(missing))
	})

	t.Run("non-ENOENT error is returned", func(t *testing.T) {
		// Unlinking a directory fails with EPERM/EISDIR, which is not
		// fs.ErrNotExist, so the error propagates.
		assert.Error(t, UnlinkSocketFiles(t.TempDir()))
	})
}

// TestRun verifies a successful command returns no error.
func TestRun(t *testing.T) {
	require.NoError(t, Run("true"))
}

// TestRunError verifies a failing command is wrapped in an ErrorWithStderr.
func TestRunError(t *testing.T) {
	err := Run("false")
	require.Error(t, err)

	var stderrErr *ErrorWithStderr
	assert.True(t, errors.As(err, &stderrErr), "expected *ErrorWithStderr, got %T", err)
}

// TestRunMissingBinary verifies an unknown binary surfaces an error.
func TestRunMissingBinary(t *testing.T) {
	assert.Error(t, Run("definitely-not-a-real-binary-xyz"))
}

// TestRunWithResult verifies stdout is captured and returned as bytes.
func TestRunWithResult(t *testing.T) {
	out, err := RunWithResult("echo", "hello world")
	require.NoError(t, err)
	assert.Equal(t, "hello world", strings.TrimSpace(string(out)))
}

// TestRunWithResultError verifies the error path of RunWithResult.
func TestRunWithResultError(t *testing.T) {
	out, err := RunWithResult("false")
	assert.Error(t, err)
	assert.Nil(t, out)
}

// TestRunWithResultReader verifies the io.Reader variant exposes stdout.
func TestRunWithResultReader(t *testing.T) {
	r, err := RunWithResultReader("echo", "from reader")
	require.NoError(t, err)

	buf := make([]byte, 64)
	n, _ := r.Read(buf) //nolint
	assert.Contains(t, string(buf[:n]), "from reader")
}

// TestRunElevatedAsRoot exercises the elevated helpers only when the test
// process is already root, in which case escalation is bypassed and the
// commands run directly. Non-root runs would shell out to sudo/pkexec, so they
// are skipped.
func TestRunElevatedAsRoot(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("not root: skipping elevated command tests to avoid invoking sudo/pkexec")
	}

	require.NoError(t, RunElevated("true"))

	out, err := RunElevatedWithResult("echo", "elevated")
	require.NoError(t, err)
	assert.Equal(t, "elevated", strings.TrimSpace(string(out)))

	r, err := RunElevatedWithResultReader("echo", "reader")
	require.NoError(t, err)
	assert.NotNil(t, r)
}

// TestGainRoot verifies privilege escalation: as a non-root process it must
// fail, and as root it returns the prior uid.
func TestGainRoot(t *testing.T) {
	uid, err := GainRoot()
	if os.Geteuid() == 0 {
		require.NoError(t, err)
		assert.GreaterOrEqual(t, uid, 0)
		return
	}
	assert.Error(t, err)
	assert.Equal(t, 0, uid)
}

// TestReleaseRoot verifies releasing to the caller's own uid succeeds (setting
// the uid to itself is always permitted).
func TestReleaseRoot(t *testing.T) {
	assert.NoError(t, ReleaseRoot(os.Getuid()))
}
