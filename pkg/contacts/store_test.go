package contacts

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
)

func TestPutListExportImport(t *testing.T) {
	s, err := OpenStore(filepath.Join(t.TempDir(), "contacts"))
	require.NoError(t, err)
	pk, _ := cipher.GenerateKeyPair()

	alice, err := s.Put(Contact{
		Name:     "Alice",
		Keys:     []cipher.PubKey{pk},
		Mail:     []string{"mail@" + pk.DNSLabel() + ".skynet"},
		Payments: []Payment{{Coin: "skycoin", Address: "2jBbGxZRGoQG1mqhPBnXnLTxK6oxsTf8os6"}},
		Groups:   []string{"skywire", "friends"},
		Notes:    "met at the meetup, line one\nline two",
	})
	require.NoError(t, err)
	require.NotEmpty(t, alice.UID)
	_, err = s.Put(Contact{Name: "bob"})
	require.NoError(t, err)

	list, err := s.List()
	require.NoError(t, err)
	require.Len(t, list, 2)
	require.Equal(t, alice, list[0], "a contact reads back as it was put")
	require.Equal(t, "bob", list[1].Name)

	var vcf bytes.Buffer
	require.NoError(t, s.Export(&vcf))
	require.Contains(t, vcf.String(), "IMPP:skychat:"+pk.Hex())

	other, err := OpenStore(filepath.Join(t.TempDir(), "other"))
	require.NoError(t, err)
	n, err := other.Import(&vcf)
	require.NoError(t, err)
	require.Equal(t, 2, n)
	got, err := other.List()
	require.NoError(t, err)
	require.Equal(t, list, got, "export then import keeps every field and UID")
}

func TestPutReplacesByUIDAndDelete(t *testing.T) {
	s, err := OpenStore(t.TempDir())
	require.NoError(t, err)
	c, err := s.Put(Contact{Name: "Alice"})
	require.NoError(t, err)
	c.Name = "Alice B"
	_, err = s.Put(c)
	require.NoError(t, err)
	list, err := s.List()
	require.NoError(t, err)
	require.Len(t, list, 1)
	require.Equal(t, "Alice B", list[0].Name)

	require.NoError(t, s.Delete(c.UID))
	require.ErrorIs(t, s.Delete(c.UID), ErrNotFound)
	_, err = s.Put(Contact{Name: "  "})
	require.Error(t, err, "a contact needs a name")
}

// A card from another address book: vCard 3.0, a UID that names no file, and a
// skywire key only as an IMPP URI.
func TestImportForeignCard(t *testing.T) {
	s, err := OpenStore(t.TempDir())
	require.NoError(t, err)
	pk, _ := cipher.GenerateKeyPair()
	card := strings.Join([]string{
		"BEGIN:VCARD", "VERSION:3.0", "UID:../../etc/passwd", "FN:Carol", "N:Carol;;;;",
		"TEL:+1 555 0100", "IMPP:skychat:" + pk.Hex(), "CATEGORIES:work,skywire",
		"END:VCARD", "",
	}, "\r\n")
	n, err := s.Import(strings.NewReader(card))
	require.NoError(t, err)
	require.Equal(t, 1, n)

	list, err := s.List()
	require.NoError(t, err)
	require.Len(t, list, 1)
	require.Equal(t, "Carol", list[0].Name)
	require.Equal(t, []cipher.PubKey{pk}, list[0].Keys)
	require.Equal(t, []string{"skywire", "work"}, list[0].Groups)
	require.FileExists(t, filepath.Join(s.Dir(), list[0].UID+".vcf"), "the UID was replaced by one that names a file in the book")

	_, err = s.Import(strings.NewReader(strings.Replace(card, "IMPP:skychat:"+pk.Hex(), "X-SKYWIRE-PK:nope", 1)))
	require.Error(t, err, "a key that does not parse fails the import")
}

func TestBookIsPrivate(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "contacts")
	s, err := OpenStore(dir)
	require.NoError(t, err)
	c, err := s.Put(Contact{Name: "Alice"})
	require.NoError(t, err)
	for path, want := range map[string]os.FileMode{dir: 0o700, filepath.Join(dir, c.UID+".vcf"): 0o600} {
		fi, err := os.Stat(path)
		require.NoError(t, err)
		require.Equal(t, want, fi.Mode().Perm(), path)
	}
}

func TestParsePayment(t *testing.T) {
	p, err := ParsePayment("Bitcoin:bc1qxy2kgdygjrsqtzq2n0yrf2493p83kkfjhx0wlh")
	require.NoError(t, err)
	require.Equal(t, Payment{Coin: "bitcoin", Address: "bc1qxy2kgdygjrsqtzq2n0yrf2493p83kkfjhx0wlh"}, p)
	for _, bad := range []string{"", "skycoin", ":addr", "sky coin:addr"} {
		_, err := ParsePayment(bad)
		require.Error(t, err, bad)
	}
}
