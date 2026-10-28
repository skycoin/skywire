// Package commands cmd/apps/skychat/commands/contacts.go c4-app-chat
//
// The address book's HTTP surface, and the one function every display path
// should use to turn a public key into something a human recognizes.
//
//	GET  /contacts          the whole book: {"<pk>": "<name>", …}
//	POST /contacts          {pk, name} — set, or forget with an empty name
//	POST /contacts/import   {names:{…}} — fill gaps only (UI migration)
//
// Why it is served rather than kept in the UI: a nickname used to live in the
// chat page's localStorage, where the two surfaces that need it most could not
// see it — a notification title, composed here in Go, and a phone's native
// call screen, which is Kotlin. Both showed hex for a person the user had
// already named. See pkg/skychat/contacts for the rest of the reasoning.
package commands

import (
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"github.com/skycoin/skywire/pkg/skychat/contacts"
)

// contactStore is the open address book, or nil when it could not be opened —
// in which case every path here degrades to the shortened key rather than
// failing, because a chat that will not start over a nickname file is worse
// than a chat that shows keys.
var contactStore *contacts.Store

// registerContactHandlers wires the address-book routes.
func registerContactHandlers(mux *http.ServeMux) {
	// "/contacts/import" is registered first so ServeMux's longest-pattern
	// match reaches it; "/contacts" is an exact path and would not shadow it,
	// but the ordering makes the intent readable.
	mux.HandleFunc("/contacts/import", requireAuthFunc(contactsImportHandler))
	mux.HandleFunc("/contacts", requireAuthFunc(contactsHandler))
}

// contactStorePath is where the book lives: beside the app's other state, in
// its own work dir, named for the identity — see identity.go for why the
// visor's key is part of the name.
func contactStorePath() string {
	return identityStatePath(stateDir(), identityScope(), "skychat-contacts.json")
}

// openContactStore opens the address book at path, logging and continuing on
// failure. Called once at startup.
func openContactStore(path string) {
	store, err := contacts.OpenStore(path)
	if err != nil {
		appLog("Contacts: address book unavailable (%v) — names will show as keys", err)
		return
	}
	contactStore = store
}

func contactsHandler(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, shownNames())
	case http.MethodPost:
		var body struct {
			PK   string `json:"pk"`
			Name string `json:"name"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "invalid body: "+err.Error(), http.StatusBadRequest)
			return
		}
		if contactStore == nil {
			http.Error(w, "address book unavailable", http.StatusServiceUnavailable)
			return
		}
		stored, err := contactStore.Set(body.PK, body.Name)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(w, map[string]string{"pk": body.PK, "name": stored})
	default:
		http.Error(w, "GET or POST only", http.StatusMethodNotAllowed)
	}
}

// contactsImportHandler takes a UI's own older store and fills what the book
// does not have yet. Never overwrites — see contacts.Store.Merge.
func contactsImportHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	var body struct {
		Names map[string]string `json:"names"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid body: "+err.Error(), http.StatusBadRequest)
		return
	}
	if contactStore == nil {
		http.Error(w, "address book unavailable", http.StatusServiceUnavailable)
		return
	}
	added, err := contactStore.Merge(body.Names)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if added > 0 {
		appLog("Contacts: imported %d name(s) from the UI's local store", added)
	}
	writeJSON(w, map[string]int{"added": added})
}

// contactNames is the whole book, never nil so the UI always gets an object.
func contactNames() map[string]string {
	if contactStore == nil {
		return map[string]string{}
	}
	return contactStore.All()
}

// displayName is what to CALL a peer anywhere a human will read it.
//
// The operator's skychat nickname wins, then the visor contact book, then the
// shortened key. A name the peer publishes about itself is not consulted here
// on purpose — the UI fetches those over the network (which a notification
// path must never do) and writes one into the address book when there is no
// nickname yet, so by the time it matters it is already the first case.
func displayName(pk string) string {
	if name := contactStore.Name(pk); name != "" {
		return name
	}
	if name := bookNames()[pk]; name != "" {
		return name
	}
	return shortHexPK(pk)
}

// bookTTL bounds how stale the visor contact book can be here. It is read on
// every notification, so it is cached rather than fetched per message.
const bookTTL = 30 * time.Second

var book struct {
	mu      sync.Mutex
	names   map[string]string
	fetched time.Time
}

// bookNames is the visor contact book as key hex to name. Standalone, or when
// the visor cannot answer, it is the last copy fetched (empty at first).
func bookNames() map[string]string {
	book.mu.Lock()
	stale := appCl != nil && time.Since(book.fetched) >= bookTTL
	if stale {
		book.fetched = time.Now()
	}
	names := book.names
	book.mu.Unlock()
	if !stale {
		return names
	}
	// Fetched outside the lock so a slow visor never stalls other callers.
	fresh, err := appCl.ContactNames()
	if err != nil {
		return names
	}
	book.mu.Lock()
	book.names = fresh
	book.mu.Unlock()
	return fresh
}

// shownNames is what the UI displays: the contact book, overlaid by skychat's
// own nicknames so a nickname still wins. The export keeps only nicknames.
func shownNames() map[string]string {
	out := make(map[string]string)
	for pk, name := range bookNames() {
		out[pk] = name
	}
	for pk, name := range contactNames() {
		out[pk] = name
	}
	return out
}
