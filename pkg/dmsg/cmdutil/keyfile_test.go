// Package cmdutil pkg/cmdutil/keyfile_test.go
package cmdutil

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
)

func TestLoadOrGenerateKey_GeneratesNew(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.key")

	var sk cipher.SecKey
	err := LoadOrGenerateKey(path, &sk)
	require.NoError(t, err)
	assert.False(t, sk.Null(), "secret key should be generated")

	// File should exist with content
	data, err := os.ReadFile(path) //nolint:gosec
	require.NoError(t, err)
	assert.NotEmpty(t, data)
}

func TestLoadOrGenerateKey_LoadsExisting(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.key")

	// Generate first
	var sk1 cipher.SecKey
	err := LoadOrGenerateKey(path, &sk1)
	require.NoError(t, err)

	// Load same file
	var sk2 cipher.SecKey
	err = LoadOrGenerateKey(path, &sk2)
	require.NoError(t, err)

	assert.Equal(t, sk1, sk2, "should load the same key")
}

func TestLoadOrGenerateKey_EmptyFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "empty.key")

	err := os.WriteFile(path, []byte(""), 0600)
	require.NoError(t, err)

	var sk cipher.SecKey
	err = LoadOrGenerateKey(path, &sk)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "empty")
}

func TestLoadOrGenerateKey_WhitespaceOnlyFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ws.key")

	err := os.WriteFile(path, []byte("  \n\t  \n"), 0600)
	require.NoError(t, err)

	var sk cipher.SecKey
	err = LoadOrGenerateKey(path, &sk)
	assert.Error(t, err)
}

func TestLoadOrGenerateKey_InvalidContent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.key")

	err := os.WriteFile(path, []byte("not-a-valid-secret-key"), 0600)
	require.NoError(t, err)

	var sk cipher.SecKey
	err = LoadOrGenerateKey(path, &sk)
	assert.Error(t, err)
}
