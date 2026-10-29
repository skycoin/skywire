package commands

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/skychat/contacts"
)

// TestNicknameBeatsContactBook checks the UI and notifications show the visor
// contact book's name only where there is no skychat nickname, and that the
// export carries nicknames alone.
func TestNicknameBeatsContactBook(t *testing.T) {
	a, _ := cipher.GenerateKeyPair()
	b, _ := cipher.GenerateKeyPair()

	store, err := contacts.OpenStore(filepath.Join(t.TempDir(), "c.json"))
	require.NoError(t, err)
	_, err = store.Set(a.Hex(), "Nick")
	require.NoError(t, err)
	oldStore := contactStore
	contactStore = store
	book.names = map[string]string{a.Hex(): "Alice", b.Hex(): "Bob"}
	t.Cleanup(func() { contactStore = oldStore; book.names = nil })

	require.Equal(t, "Nick", displayName(a.Hex()))
	require.Equal(t, "Bob", displayName(b.Hex()))
	require.Equal(t, map[string]string{a.Hex(): "Nick", b.Hex(): "Bob"}, shownNames())
	require.Equal(t, map[string]string{a.Hex(): "Nick"}, contactNames())
}
