// Package commands cmd/apps/skychat/commands/transfer_test.go
//
// The export/import surface: what an archive carries, what it refuses, and
// the round trip that is the whole point — a file written by one visor read
// back into another with nothing lost and nothing doubled.
//
// These toggle the package-level historyStore/contactStore globals, so each
// test installs its own under t.TempDir() and restores what was there.
package commands

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/skycoin/skywire/pkg/skychat/contacts"
	"github.com/skycoin/skywire/pkg/skychat/history"
)

// withAppLog installs a no-op appLog: the handlers log their counts, and the
// package var is nil until RunSkychat wires it.
func withAppLog(t *testing.T) {
	t.Helper()
	prev := appLog
	appLog = func(string, ...interface{}) {}
	t.Cleanup(func() { appLog = prev })
}

// withContactStore installs a fresh address book for the test.
func withContactStore(t *testing.T) *contacts.Store {
	t.Helper()
	prev := contactStore
	store, err := contacts.OpenStore(filepath.Join(t.TempDir(), "contacts.json"))
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	contactStore = store
	t.Cleanup(func() { contactStore = prev })
	return store
}

// transferFixture wires an empty history store, an empty address book and a
// working appLog, and hands back the store so a test can seed it.
func transferFixture(t *testing.T) history.Store {
	t.Helper()
	withAppLog(t)
	withContactStore(t)
	restoreHistoryStore(t)
	historyStore = newTempStore(t, history.DefaultLimits())
	return historyStore
}

func peerHex(prefix string) string {
	return prefix + strings.Repeat("0", 66-len(prefix))
}

func doExport(t *testing.T, path string) (*httptest.ResponseRecorder, archive) {
	t.Helper()
	rec := httptest.NewRecorder()
	exportHandler(rec, httptest.NewRequest(http.MethodGet, path, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("export status %d: %s", rec.Code, rec.Body.String())
	}
	var out archive
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("export body: %v", err)
	}
	return rec, out
}

func doImport(t *testing.T, in any) (*httptest.ResponseRecorder, importReport) {
	t.Helper()
	raw, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal archive: %v", err)
	}
	rec := httptest.NewRecorder()
	importHandler(rec, httptest.NewRequest(http.MethodPost, "/import", bytes.NewReader(raw)))
	var report importReport
	// A refusal answers plain text; the caller checks the code for those.
	_ = json.Unmarshal(rec.Body.Bytes(), &report) //nolint:errcheck
	return rec, report
}

func TestExport_CarriesHistoryAndContacts(t *testing.T) {
	store := transferFixture(t)
	peer := peerHex("03aa")
	at := time.Now().UTC().Add(-time.Hour)

	if err := store.Append(history.Message{
		Peer: peer, Text: "hello", ID: "m1", Timestamp: at,
	}); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if err := store.AppendGroup(history.GroupMessage{
		GroupID: "g1", SenderPK: peer, Text: "in the group", Timestamp: at,
	}); err != nil {
		t.Fatalf("AppendGroup: %v", err)
	}
	if _, err := contactStore.Set(peer, "Friend"); err != nil {
		t.Fatalf("contacts.Set: %v", err)
	}

	rec, out := doExport(t, "/export")

	if out.Format != archiveFormat || out.Version != archiveVersion {
		t.Fatalf("format/version = %q/%d", out.Format, out.Version)
	}
	if len(out.Messages) != 1 || out.Messages[0].Text != "hello" {
		t.Fatalf("messages = %+v", out.Messages)
	}
	if len(out.GroupMessages) != 1 || out.GroupMessages[0].Text != "in the group" {
		t.Fatalf("group messages = %+v", out.GroupMessages)
	}
	if out.Contacts[peer] != "Friend" {
		t.Fatalf("contacts = %v", out.Contacts)
	}
	// Offered as a download, not rendered: a browser that navigates to this
	// must save it rather than show a wall of JSON.
	if cd := rec.Header().Get("Content-Disposition"); !strings.HasPrefix(cd, "attachment;") {
		t.Fatalf("Content-Disposition = %q", cd)
	}
}

// Persistence off is not "nothing to export": the address book is still real
// data, and it is the half that survives on a visor with no history store.
func TestExport_WithoutPersistenceStillCarriesContacts(t *testing.T) {
	withAppLog(t)
	withContactStore(t)
	restoreHistoryStore(t)
	historyStore = nil

	peer := peerHex("03bb")
	if _, err := contactStore.Set(peer, "Only a name"); err != nil {
		t.Fatalf("contacts.Set: %v", err)
	}

	_, out := doExport(t, "/export")
	if out.Contacts[peer] != "Only a name" {
		t.Fatalf("contacts = %v", out.Contacts)
	}
	if len(out.Messages) != 0 {
		t.Fatalf("messages = %+v, want none", out.Messages)
	}
}

// The path suffix exists only so a client that names a download from the URL
// gets a .json; the handler must ignore it.
func TestExport_IgnoresTheFilenameInThePath(t *testing.T) {
	transferFixture(t)
	_, out := doExport(t, "/export/skychat-2026-08-06.json")
	if out.Format != archiveFormat {
		t.Fatalf("format = %q", out.Format)
	}
}

func TestImport_MergesAndIsIdempotent(t *testing.T) {
	transferFixture(t)
	peer := peerHex("03cc")
	at := time.Now().UTC().Add(-time.Hour)

	in := archive{
		Format:   archiveFormat,
		Version:  archiveVersion,
		Contacts: map[string]string{peer: "Desktop"},
		Messages: []history.Message{
			{Peer: peer, Text: "one", ID: "m1", Timestamp: at},
			{Peer: peer, Text: "two", ID: "m2", Timestamp: at.Add(time.Second)},
		},
		GroupMessages: []history.GroupMessage{
			{GroupID: "g1", SenderPK: peer, Text: "group", Timestamp: at},
		},
	}

	rec, report := doImport(t, in)
	if rec.Code != http.StatusOK {
		t.Fatalf("import status %d: %s", rec.Code, rec.Body.String())
	}
	if report.Messages != 2 || report.GroupMessages != 1 || report.Contacts != 1 {
		t.Fatalf("report = %+v", report)
	}
	if !report.Persistence {
		t.Fatal("persistence should be reported on")
	}

	// The same file again changes nothing — the property someone leans on
	// when they are not sure whether the first import worked.
	_, again := doImport(t, in)
	if again.Messages != 0 || again.GroupMessages != 0 || again.Duplicates != 3 {
		t.Fatalf("re-import report = %+v", again)
	}
}

// The migration this whole file exists for: everything one visor exports is
// what another ends up holding.
func TestExportImport_RoundTrip(t *testing.T) {
	source := transferFixture(t)
	peer := peerHex("03dd")
	at := time.Now().UTC().Add(-time.Hour)

	for i, text := range []string{"first", "second", "third"} {
		if err := source.Append(history.Message{
			Peer:      peer,
			Text:      text,
			ID:        "m" + string(rune('1'+i)),
			Timestamp: at.Add(time.Duration(i) * time.Second),
		}); err != nil {
			t.Fatalf("Append %s: %v", text, err)
		}
	}
	if _, err := contactStore.Set(peer, "Carried over"); err != nil {
		t.Fatalf("contacts.Set: %v", err)
	}

	_, exported := doExport(t, "/export")

	// A second device: fresh store, fresh book, same handlers.
	withContactStore(t)
	historyStore = newTempStore(t, history.DefaultLimits())

	rec, report := doImport(t, exported)
	if rec.Code != http.StatusOK {
		t.Fatalf("import status %d: %s", rec.Code, rec.Body.String())
	}
	if report.Messages != 3 || report.Contacts != 1 {
		t.Fatalf("report = %+v", report)
	}

	got, err := historyStore.ListByPeer(peer, 0)
	if err != nil {
		t.Fatalf("ListByPeer: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("second device holds %d messages, want 3", len(got))
	}
	for i, want := range []string{"first", "second", "third"} {
		if got[i].Text != want {
			t.Fatalf("message %d = %q, want %q (order must survive)", i, got[i].Text, want)
		}
	}
	if contactStore.Name(peer) != "Carried over" {
		t.Fatalf("name = %q", contactStore.Name(peer))
	}
}

// A name already set on this device wins: the archive fills gaps, it does
// not overwrite what the operator chose here.
func TestImport_NeverOverwritesALocalName(t *testing.T) {
	transferFixture(t)
	peer := peerHex("03ee")

	if _, err := contactStore.Set(peer, "My name for them"); err != nil {
		t.Fatalf("contacts.Set: %v", err)
	}

	if _, report := doImport(t, archive{
		Format:   archiveFormat,
		Version:  archiveVersion,
		Contacts: map[string]string{peer: "The old device's name"},
	}); report.Contacts != 0 {
		t.Fatalf("report = %+v, want no contacts added", report)
	}
	if got := contactStore.Name(peer); got != "My name for them" {
		t.Fatalf("name = %q — the archive overwrote a local choice", got)
	}
}

func TestImport_RefusesWhatIsNotAnArchive(t *testing.T) {
	transferFixture(t)

	cases := []struct {
		name string
		body string
		want string
	}{
		{"unrelated json", `{"hello":"world"}`, "not a SkyChat archive"},
		{"unreadable", `not json at all`, "not a readable archive"},
		{"from the future", `{"format":"skychat-archive","version":99}`, "version 99"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			importHandler(rec, httptest.NewRequest(
				http.MethodPost, "/import", strings.NewReader(tc.body)))
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status %d, want 400", rec.Code)
			}
			if !strings.Contains(rec.Body.String(), tc.want) {
				t.Fatalf("body %q, want it to mention %q", rec.Body.String(), tc.want)
			}
		})
	}
}

// With no history store the messages have nowhere to go, and saying so is
// the difference between "imported nothing" and "this visor keeps no
// history — start it with --persist".
func TestImport_ReportsWhenThereIsNoHistoryStore(t *testing.T) {
	withAppLog(t)
	withContactStore(t)
	restoreHistoryStore(t)
	historyStore = nil

	peer := peerHex("03ff")
	rec, report := doImport(t, archive{
		Format:   archiveFormat,
		Version:  archiveVersion,
		Contacts: map[string]string{peer: "Still saved"},
		Messages: []history.Message{{Peer: peer, Text: "lost", ID: "m1", Timestamp: time.Now().UTC()}},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	if report.Persistence {
		t.Fatal("persistence should be reported off")
	}
	if report.Messages != 0 {
		t.Fatalf("stored %d messages without a store", report.Messages)
	}
	if report.Contacts != 1 {
		t.Fatalf("contacts = %d — the address book works without persistence", report.Contacts)
	}
}

func TestTransferHandlers_MethodGuards(t *testing.T) {
	transferFixture(t)

	rec := httptest.NewRecorder()
	exportHandler(rec, httptest.NewRequest(http.MethodPost, "/export", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST /export = %d, want 405", rec.Code)
	}

	rec = httptest.NewRecorder()
	importHandler(rec, httptest.NewRequest(http.MethodGet, "/import", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET /import = %d, want 405", rec.Code)
	}
}
