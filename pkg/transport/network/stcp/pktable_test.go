// Package stcp pkg/transport/network/stcp/pktable_test.go: unit tests for the
// public-key-to-address table backing skywire-tcp.
package stcp

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
)

// TestNewTable verifies that NewTable indexes entries in both directions and
// reports the correct count.
func TestNewTable(t *testing.T) {
	pk1, _ := cipher.GenerateKeyPair()
	pk2, _ := cipher.GenerateKeyPair()

	mt := NewTable(map[cipher.PubKey]string{
		pk1: "127.0.0.1:1000",
		pk2: "127.0.0.1:2000",
	})

	assert.Equal(t, 2, mt.Count())

	addr, ok := mt.Addr(pk1)
	assert.True(t, ok)
	assert.Equal(t, "127.0.0.1:1000", addr)

	gotPK, ok := mt.PubKey("127.0.0.1:2000")
	assert.True(t, ok)
	assert.Equal(t, pk2, gotPK)
}

// TestNewTableEmpty verifies the lookups on an empty table miss cleanly.
func TestNewTableEmpty(t *testing.T) {
	pk, _ := cipher.GenerateKeyPair()
	mt := NewTable(map[cipher.PubKey]string{})

	assert.Equal(t, 0, mt.Count())

	_, ok := mt.Addr(pk)
	assert.False(t, ok)

	_, ok = mt.PubKey("127.0.0.1:1000")
	assert.False(t, ok)
}

// TestSetAddr verifies SetAddr registers an entry that is then resolvable in
// both directions and reflected in the count.
func TestSetAddr(t *testing.T) {
	pk, _ := cipher.GenerateKeyPair()
	mt := NewTable(map[cipher.PubKey]string{})

	mt.SetAddr(pk, "127.0.0.1:3000")

	assert.Equal(t, 1, mt.Count())

	addr, ok := mt.Addr(pk)
	assert.True(t, ok)
	assert.Equal(t, "127.0.0.1:3000", addr)

	gotPK, ok := mt.PubKey("127.0.0.1:3000")
	assert.True(t, ok)
	assert.Equal(t, pk, gotPK)
}

// TestNewTableFromFile verifies a well-formed table file is parsed into a
// working table.
func TestNewTableFromFile(t *testing.T) {
	pk1, _ := cipher.GenerateKeyPair()
	pk2, _ := cipher.GenerateKeyPair()

	contents := pk1.String() + " 127.0.0.1:1000\n" +
		pk2.String() + " 127.0.0.1:2000\n"

	path := filepath.Join(t.TempDir(), "pk_table.txt")
	require.NoError(t, os.WriteFile(path, []byte(contents), 0600))

	mt, err := NewTableFromFile(path)
	require.NoError(t, err)

	assert.Equal(t, 2, mt.Count())

	addr, ok := mt.Addr(pk1)
	assert.True(t, ok)
	assert.Equal(t, "127.0.0.1:1000", addr)

	gotPK, ok := mt.PubKey("127.0.0.1:2000")
	assert.True(t, ok)
	assert.Equal(t, pk2, gotPK)
}

// TestNewTableFromFileMissing verifies a non-existent path returns an error.
func TestNewTableFromFileMissing(t *testing.T) {
	_, err := NewTableFromFile(filepath.Join(t.TempDir(), "does_not_exist.txt"))
	assert.Error(t, err)
}

// TestNewTableFromFileBadFieldCount verifies a line without exactly two fields
// is rejected.
func TestNewTableFromFileBadFieldCount(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pk_table.txt")
	require.NoError(t, os.WriteFile(path, []byte("only-one-field\n"), 0600))

	_, err := NewTableFromFile(path)
	assert.Error(t, err)
}

// TestNewTableFromFileBadPubKey verifies an unparseable public key is rejected.
func TestNewTableFromFileBadPubKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pk_table.txt")
	require.NoError(t, os.WriteFile(path, []byte("not-a-pubkey 127.0.0.1:1000\n"), 0600))

	_, err := NewTableFromFile(path)
	assert.Error(t, err)
}
