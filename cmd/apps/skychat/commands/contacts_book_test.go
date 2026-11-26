package commands

import (
	"encoding/json"
	"maps"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

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

// fakeVisorBook stands in for the visor contact book, keyed by key hex.
type fakeVisorBook struct{ names map[string]string }

func (b *fakeVisorBook) ContactNames() (map[string]string, error) {
	out := make(map[string]string, len(b.names))
	maps.Copy(out, b.names)
	return out, nil
}

func (b *fakeVisorBook) ChatContactSave(pk cipher.PubKey, name string) (bool, error) {
	if _, ok := b.names[pk.Hex()]; ok {
		return false, nil
	}
	b.names[pk.Hex()] = name
	return true, nil
}

func (b *fakeVisorBook) ChatContactForget(pk cipher.PubKey) error {
	delete(b.names, pk.Hex())
	return nil
}

func withVisorBook(t *testing.T, vb visorBookAPI) {
	t.Helper()
	prev := visorBook
	visorBook = func() visorBookAPI { return vb }
	t.Cleanup(func() {
		visorBook = prev
		book.mu.Lock()
		book.names, book.fetched = nil, time.Time{}
		book.mu.Unlock()
	})
}

func serveBook(t *testing.T, h http.HandlerFunc, method, target, body string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	h(rec, httptest.NewRequest(method, target, strings.NewReader(body)))
	return rec
}

// TestContactsBookIsTheVisorBook checks the Skychat address book lists, adds
// to and removes from the visor contact book, so a contact added on the
// hypervisor's Contacts page shows in Skychat and the other way round.
func TestContactsBookIsTheVisorBook(t *testing.T) {
	withContactStore(t)
	honor, _ := cipher.GenerateKeyPair()
	alice, _ := cipher.GenerateKeyPair()
	vb := &fakeVisorBook{names: map[string]string{honor.Hex(): "Honor 70"}}
	withVisorBook(t, vb)

	var got struct {
		Available bool              `json:"available"`
		Contacts  map[string]string `json:"contacts"`
	}
	rec := serveBook(t, contactsBookHandler, http.MethodGet, "/contacts/book", "")
	require.Equal(t, http.StatusOK, rec.Code)
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	require.True(t, got.Available)
	require.Equal(t, map[string]string{honor.Hex(): "Honor 70"}, got.Contacts, "a contact added on the hypervisor is listed")

	rec = serveBook(t, contactsBookHandler, http.MethodPost, "/contacts/book", `{"pk":"`+alice.Hex()+`","name":"Alice"}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Equal(t, "Alice", vb.names[alice.Hex()], "a contact added in Skychat lands in the visor book")
	require.Equal(t, "Alice", displayName(alice.Hex()), "and is named at once, not after the cache expires")

	rec = serveBook(t, contactsBookHandler, http.MethodPost, "/contacts/book", `{"pk":"`+honor.Hex()+`","name":"Renamed"}`)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "Honor 70", vb.names[honor.Hex()], "saving a held key never renames the visor contact")

	rec = serveBook(t, contactsBookHandler, http.MethodDelete, "/contacts/book?pk="+honor.Hex(), "")
	require.Equal(t, http.StatusOK, rec.Code)
	require.NotContains(t, vb.names, honor.Hex(), "removing in Skychat removes from the visor book")

	rec = serveBook(t, contactsBookHandler, http.MethodPost, "/contacts/book", `{"pk":"nope"}`)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

// TestContactsBookImportFillsGaps checks the page's older saved list moves
// into the visor book without renaming a contact the book already holds, and
// that a key with no name gets the nickname or the shortened key.
func TestContactsBookImportFillsGaps(t *testing.T) {
	store := withContactStore(t)
	held, _ := cipher.GenerateKeyPair()
	nicked, _ := cipher.GenerateKeyPair()
	bare, _ := cipher.GenerateKeyPair()
	_, err := store.Set(nicked.Hex(), "Nick")
	require.NoError(t, err)
	vb := &fakeVisorBook{names: map[string]string{held.Hex(): "Held"}}
	withVisorBook(t, vb)

	body := `{"pks":["` + held.Hex() + `","` + nicked.Hex() + `","` + bare.Hex() + `","junk"]}`
	rec := serveBook(t, contactsBookImportHandler, http.MethodPost, "/contacts/book/import", body)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.JSONEq(t, `{"added":2}`, rec.Body.String())
	require.Equal(t, map[string]string{
		held.Hex():   "Held",
		nicked.Hex(): "Nick",
		bare.Hex():   shortHexPK(bare.Hex()),
	}, vb.names)
}

// TestContactsBookStandalone checks a skychat with no visor says so rather
// than pretending the book is empty, so the page keeps its own list.
func TestContactsBookStandalone(t *testing.T) {
	withVisorBook(t, nil)
	rec := serveBook(t, contactsBookHandler, http.MethodGet, "/contacts/book", "")
	require.JSONEq(t, `{"available":false,"contacts":{}}`, rec.Body.String())
	pk, _ := cipher.GenerateKeyPair()
	rec = serveBook(t, contactsBookHandler, http.MethodPost, "/contacts/book", `{"pk":"`+pk.Hex()+`"}`)
	require.Equal(t, http.StatusServiceUnavailable, rec.Code)
	rec = serveBook(t, contactsBookImportHandler, http.MethodPost, "/contacts/book/import", `{"pks":[]}`)
	require.Equal(t, http.StatusServiceUnavailable, rec.Code)
}
