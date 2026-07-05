// Package rename pkg/util/rename/rename_test.go: unit tests for the cross-device
// aware file rename helper.
package rename

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// crossDeviceDir returns a writable directory located on a different filesystem
// than dir, so that os.Rename between the two fails with syscall.EXDEV. It
// returns ("", false) when no such filesystem is available. /dev/shm is a
// tmpfs on Linux; to verify locally on macOS, create a RAM disk and add its
// mount point (e.g. /Volumes/RAMDisk) to the candidate list below.
func crossDeviceDir(t *testing.T, dir string) (string, bool) {
	t.Helper()
	for _, cand := range []string{"/dev/shm"} {
		info, err := os.Stat(cand)
		if err != nil || !info.IsDir() {
			continue
		}
		probeDir, err := os.MkdirTemp(cand, "rename-probe")
		if err != nil {
			continue
		}
		t.Cleanup(func() { _ = os.RemoveAll(probeDir) }) //nolint

		src := filepath.Join(dir, "probe-src")
		if err := os.WriteFile(src, []byte("p"), 0600); err != nil {
			continue
		}
		err = os.Rename(src, filepath.Join(probeDir, "probe-dst"))
		_ = os.Remove(src) //nolint
		if errors.Is(err, syscall.EXDEV) {
			return probeDir, true
		}
	}
	return "", false
}

// TestRenameSameDevice verifies a normal (same-filesystem) rename moves the file.
func TestRenameSameDevice(t *testing.T) {
	dir := t.TempDir()
	oldPath := filepath.Join(dir, "old.txt")
	newPath := filepath.Join(dir, "new.txt")
	require.NoError(t, os.WriteFile(oldPath, []byte("payload"), 0600))

	require.NoError(t, Rename(oldPath, newPath))

	assert.NoFileExists(t, oldPath)
	got, err := os.ReadFile(newPath) //nolint
	require.NoError(t, err)
	assert.Equal(t, "payload", string(got))
}

// TestRenameError verifies a non-cross-device failure (missing source) is
// returned directly.
func TestRenameError(t *testing.T) {
	dir := t.TempDir()
	err := Rename(filepath.Join(dir, "missing.txt"), filepath.Join(dir, "dst.txt"))
	assert.Error(t, err)
}

// TestRenameCrossDevice exercises the copy+remove workaround taken when
// os.Rename fails with a cross-device link error. It is skipped on platforms
// where a second filesystem is not available to provoke that error.
func TestRenameCrossDevice(t *testing.T) {
	dir := t.TempDir()
	otherDir, ok := crossDeviceDir(t, dir)
	if !ok {
		t.Skip("no second filesystem available to trigger a cross-device rename")
	}

	oldPath := filepath.Join(dir, "old.txt")
	newPath := filepath.Join(otherDir, "new.txt")
	require.NoError(t, os.WriteFile(oldPath, []byte("cross"), 0o640)) //nolint

	require.NoError(t, Rename(oldPath, newPath))

	assert.NoFileExists(t, oldPath)
	got, err := os.ReadFile(newPath) //nolint
	require.NoError(t, err)
	assert.Equal(t, "cross", string(got))

	// Mode should be preserved by the chmod step.
	info, err := os.Stat(newPath)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o640), info.Mode().Perm())

	// A non-regular source (directory) across devices is rejected: the copy
	// workaround only handles regular files.
	srcDir := filepath.Join(dir, "a-dir")
	require.NoError(t, os.Mkdir(srcDir, 0o750))
	assert.Error(t, Rename(srcDir, filepath.Join(otherDir, "a-dir")))
}

// TestMove verifies the copy-based move helper, including its open and create
// error paths.
func TestMove(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		dir := t.TempDir()
		oldPath := filepath.Join(dir, "src.txt")
		newPath := filepath.Join(dir, "dst.txt")
		require.NoError(t, os.WriteFile(oldPath, []byte("data"), 0600))

		require.NoError(t, move(oldPath, newPath))

		// move copies; it does not remove the source (Rename does that).
		assert.FileExists(t, oldPath)
		got, err := os.ReadFile(newPath) //nolint
		require.NoError(t, err)
		assert.Equal(t, "data", string(got))
	})

	t.Run("open error", func(t *testing.T) {
		dir := t.TempDir()
		err := move(filepath.Join(dir, "nope.txt"), filepath.Join(dir, "dst.txt"))
		assert.Error(t, err)
	})

	t.Run("create error", func(t *testing.T) {
		dir := t.TempDir()
		oldPath := filepath.Join(dir, "src.txt")
		require.NoError(t, os.WriteFile(oldPath, []byte("data"), 0600))

		// Destination lives under a path component that is not a directory.
		err := move(oldPath, filepath.Join(oldPath, "child", "dst.txt"))
		assert.Error(t, err)
	})
}
