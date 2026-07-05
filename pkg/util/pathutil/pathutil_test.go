// Package pathutil pkg/util/pathutil/pathutil_test.go: unit tests for the path
// existence, directory and atomic-write helpers.
package pathutil

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestExists verifies Exists reports true for existing paths (file and dir) and
// false for missing ones.
func TestExists(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "f.txt")
	require.NoError(t, os.WriteFile(file, []byte("x"), 0600))

	assert.True(t, Exists(file))
	assert.True(t, Exists(dir))
	assert.False(t, Exists(filepath.Join(dir, "missing")))
}

// TestEnsureDir verifies a missing directory is created and an existing one is
// left untouched.
func TestEnsureDir(t *testing.T) {
	base := t.TempDir()

	t.Run("creates missing", func(t *testing.T) {
		target := filepath.Join(base, "a", "b", "c")
		require.NoError(t, EnsureDir(target))
		assert.True(t, Exists(target))
	})

	t.Run("existing is a no-op", func(t *testing.T) {
		assert.NoError(t, EnsureDir(base))
	})
}

// TestAtomicWriteFile verifies data is written, and that an existing target and
// leftover temp file are both handled on overwrite.
func TestAtomicWriteFile(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "data.bin")

	require.NoError(t, AtomicWriteFile(target, []byte("first")))
	got, err := os.ReadFile(target) //nolint
	require.NoError(t, err)
	assert.Equal(t, "first", string(got))

	// Overwrite: the existing target must be replaced.
	require.NoError(t, AtomicWriteFile(target, []byte("second")))
	got, err = os.ReadFile(target) //nolint
	require.NoError(t, err)
	assert.Equal(t, "second", string(got))
}

// TestAtomicWriteFileStaleTempFile verifies a leftover ".tmp" file from a
// previously interrupted write is cleaned up and the write succeeds.
func TestAtomicWriteFileStaleTempFile(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "data.bin")
	require.NoError(t, AtomicWriteFile(target, []byte("existing")))
	require.NoError(t, os.WriteFile(target+tmpSuffix, []byte("stale"), 0600))

	require.NoError(t, AtomicWriteFile(target, []byte("new")))
	got, err := os.ReadFile(target) //nolint
	require.NoError(t, err)
	assert.Equal(t, "new", string(got))
	assert.False(t, Exists(target+tmpSuffix), "temp file should be consumed by the rename")
}

// TestAtomicAppendToFile verifies new data is appended to the existing contents.
func TestAtomicAppendToFile(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "log.txt")
	require.NoError(t, AtomicWriteFile(target, []byte("a")))

	require.NoError(t, AtomicAppendToFile(target, []byte("b")))
	require.NoError(t, AtomicAppendToFile(target, []byte("c")))

	got, err := os.ReadFile(target) //nolint
	require.NoError(t, err)
	assert.Equal(t, "abc", string(got))
}

// TestAtomicAppendToFileMissing verifies appending to a non-existent file
// surfaces the read error.
func TestAtomicAppendToFileMissing(t *testing.T) {
	err := AtomicAppendToFile(filepath.Join(t.TempDir(), "nope.txt"), []byte("x"))
	assert.Error(t, err)
}
