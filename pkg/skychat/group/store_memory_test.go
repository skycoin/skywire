//go:build js

// Package group pkg/skychat/group/store_memory_test.go c4-app-chat
package group

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
)

// A member's groups and keys come back after the store is reopened from its
// file, as they must after a tab reload.
func TestMemoryStore_ReopenKeepsRecords(t *testing.T) {
	path := filepath.Join(t.TempDir(), "skychat", "groups.json")
	sk := testStoreSK()
	owner, _ := cipher.GenerateKeyPair()
	requester, _ := cipher.GenerateKeyPair()

	st, err := OpenStore(path, sk)
	require.NoError(t, err)
	rec := Record{ID: "9861f2b2-6c70-4de9-a97b-f72236957d56", Name: "g", OwnerPK: owner, AESKey: make([]byte, 32), Status: StatusPending}
	rec.AESKey[0] = 7
	require.NoError(t, st.Put(rec))
	require.NoError(t, st.SetStatus(rec.ID, StatusActive))
	require.NoError(t, st.PutJoinRequest(JoinRequest{GroupID: rec.ID, PK: requester}))
	require.NoError(t, st.Put(Record{ID: "gone", OwnerPK: owner}))
	require.NoError(t, st.Delete("gone"))

	st2, err := OpenStore(path, sk)
	require.NoError(t, err)
	got, ok, err := st2.Get(rec.ID)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, StatusActive, got.Status)
	require.Equal(t, rec.AESKey, got.AESKey)
	_, ok, err = st2.Get("gone")
	require.NoError(t, err)
	require.False(t, ok)
	_, ok, err = st2.GetJoinRequest(rec.ID, requester)
	require.NoError(t, err)
	require.True(t, ok)

	require.NoError(t, st2.DeleteJoinRequests(rec.ID))
	st3, err := OpenStore(path, sk)
	require.NoError(t, err)
	reqs, err := st3.ListJoinRequests(rec.ID)
	require.NoError(t, err)
	require.Empty(t, reqs)
}

// Without a path nothing is written and a new store starts empty.
func TestMemoryStore_NoPathStaysInMemory(t *testing.T) {
	owner, _ := cipher.GenerateKeyPair()
	st, err := OpenStore("", testStoreSK())
	require.NoError(t, err)
	require.NoError(t, st.Put(Record{ID: "a", OwnerPK: owner}))
	st2, err := OpenStore("", testStoreSK())
	require.NoError(t, err)
	list, err := st2.List()
	require.NoError(t, err)
	require.Empty(t, list)
}
