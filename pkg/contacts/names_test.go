package contacts

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
)

func TestNamesFirstContactWins(t *testing.T) {
	a, _ := cipher.GenerateKeyPair()
	b, _ := cipher.GenerateKeyPair()
	list := []Contact{
		{Name: "Alice", Keys: []cipher.PubKey{a, b}},
		{Name: "Bob", Keys: []cipher.PubKey{b}},
	}
	require.Equal(t, map[string]string{a.Hex(): "Alice", b.Hex(): "Alice"}, Names(list))
}

func TestKeyByName(t *testing.T) {
	a, _ := cipher.GenerateKeyPair()
	b, _ := cipher.GenerateKeyPair()
	list := []Contact{
		{Name: "Alice", Keys: []cipher.PubKey{a}},
		{Name: "Bob", Keys: []cipher.PubKey{a, b}},
		{Name: "Carol"},
		{Name: "dup", Keys: []cipher.PubKey{a}},
		{Name: "Dup", Keys: []cipher.PubKey{b}},
	}
	pk, err := KeyByName(list, "alice")
	require.NoError(t, err)
	require.Equal(t, a, pk)

	for _, name := range []string{"Bob", "Carol", "dup", "Eve"} {
		_, err := KeyByName(list, name)
		require.Error(t, err, name)
	}
}
