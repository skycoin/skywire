package contacts

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
)

func TestKeySaveKeepsExistingContact(t *testing.T) {
	s, err := OpenStore(t.TempDir())
	require.NoError(t, err)
	pk, _ := cipher.GenerateKeyPair()

	added, err := s.KeySave(pk, "Honor 70")
	require.NoError(t, err)
	require.True(t, added)
	added, err = s.KeySave(pk, "someone else")
	require.NoError(t, err)
	require.False(t, added, "a key already in the book is not saved twice")

	list, err := s.List()
	require.NoError(t, err)
	require.Len(t, list, 1)
	require.Equal(t, "Honor 70", list[0].Name, "saving a key never renames its contact")

	_, err = s.KeySave(pk, " ")
	require.NoError(t, err, "the key is held, so a missing name does not matter")
	other, _ := cipher.GenerateKeyPair()
	_, err = s.KeySave(other, "")
	require.Error(t, err, "a new contact needs a name")
}

func TestKeyForget(t *testing.T) {
	s, err := OpenStore(t.TempDir())
	require.NoError(t, err)
	pk, _ := cipher.GenerateKeyPair()
	pk2, _ := cipher.GenerateKeyPair()
	mail := "mail@" + pk.DNSLabel() + ".skynet"
	mailOfKey := func(addr string) bool { return strings.Contains(addr, "@"+pk.DNSLabel()+".") }

	_, err = s.Put(Contact{Name: "only", Keys: []cipher.PubKey{pk}, Mail: []string{mail}})
	require.NoError(t, err)
	_, err = s.Put(Contact{Name: "two keys", Keys: []cipher.PubKey{pk, pk2}})
	require.NoError(t, err)
	_, err = s.Put(Contact{Name: "paid", Keys: []cipher.PubKey{pk}, Payments: []Payment{{Coin: "skycoin", Address: "2jBb"}}})
	require.NoError(t, err)
	_, err = s.Put(Contact{Name: "other", Keys: []cipher.PubKey{pk2}})
	require.NoError(t, err)

	require.NoError(t, s.KeyForget(pk, mailOfKey))
	list, err := s.List()
	require.NoError(t, err)
	require.Len(t, list, 3, "the contact that held only the key and its mailbox is gone")
	for _, c := range list {
		require.False(t, c.OwnsKey(pk), c.Name)
	}
	require.Equal(t, "other", list[0].Name)
	require.Equal(t, "paid", list[1].Name, "a contact with a payment address stays")
	require.Equal(t, []cipher.PubKey{pk2}, list[2].Keys)
	require.NoError(t, s.KeyForget(pk, nil), "forgetting a key that is gone is not an error")
}
