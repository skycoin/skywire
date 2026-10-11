// Package commands ui_delete_chat_test.go
package commands

import (
	"io"
	"regexp"
	"strings"
	"testing"
)

// TestDeleteChatReachesTheStore pins that "Delete chat" asks the visor to
// erase the stored conversation, not only the page's copy of it.
//
// The page is not the durable copy: on every load it re-adds the peers the
// store names (syncHistoryPeers) and refills an empty thread from it on open
// (loadHistoryFor). deleteChat used to call itself "a UI-local action", and
// the deleted conversation was back on the next load — on a phone, on every
// return to the chat tab.
func TestDeleteChatReachesTheStore(t *testing.T) {
	f, err := getFileSystem().Open("index.html")
	if err != nil {
		t.Fatalf("open embedded index.html: %v", err)
	}
	defer func() { _ = f.Close() }() //nolint:errcheck
	b, err := io.ReadAll(f)
	if err != nil {
		t.Fatalf("read index.html: %v", err)
	}
	// A Windows checkout may turn the file into CRLF.
	page := strings.ReplaceAll(string(b), "\r\n", "\n")

	// The body of deleteChat: from its declaration to the next method.
	body := deleteChatBody(t, page)
	if !strings.Contains(body, "_forgetStoredConversation(pk)") {
		t.Error("deleteChat no longer erases the stored conversation; the visor's history will put it back on the next load")
	}
	if strings.Contains(body, "UI-local action") {
		t.Error("deleteChat still describes itself as UI-local; it is not")
	}

	m := regexp.MustCompile(`(?s)_forgetStoredConversation\(pk\) \{\n(.*?)\n\s+\}\n`).FindStringSubmatch(page)
	if m == nil {
		t.Fatal("_forgetStoredConversation not found in index.html")
	}
	helper := m[1]
	for _, want := range []string{"'/history/forget'", "method: 'POST'", "all: true"} {
		if !strings.Contains(helper, want) {
			t.Errorf("_forgetStoredConversation lacks %s; forgetHandler needs {pk, all: true} to erase the conversation", want)
		}
	}
}

// TestDeletePairedChatStaysDeleted pins the CXO half of "Delete chat" on a
// paired conversation. Unpairing keeps the pair record (status "revoked") for
// audit, /pair still lists it, and the page re-adds every listed peer on
// every load — which, on a phone where the page reloads on every return to
// the chat tab, put the deleted conversation straight back. That read as
// "chats cannot be deleted when CXO pairing is available".
func TestDeletePairedChatStaysDeleted(t *testing.T) {
	f, err := getFileSystem().Open("index.html")
	if err != nil {
		t.Fatalf("open embedded index.html: %v", err)
	}
	defer func() { _ = f.Close() }() //nolint:errcheck
	b, err := io.ReadAll(f)
	if err != nil {
		t.Fatalf("read index.html: %v", err)
	}
	// A Windows checkout may turn the file into CRLF, and the anchors below
	// are written with \n — the Windows lane failed on exactly that.
	page := strings.ReplaceAll(string(b), "\r\n", "\n")

	// syncPairedFromVisor: the revoked check must come before the peer is
	// added to pairedSet (or re-added as a recipient). Anchored past the
	// method's end (blank line + next comment) so inner closers don't
	// truncate the body.
	m := regexp.MustCompile(`(?s)\n\s+syncPairedFromVisor\(\) \{\n(.*?)\n\s+\}\n\n\s+//`).FindStringSubmatch(page)
	if m == nil {
		t.Fatal("syncPairedFromVisor not found in index.html")
	}
	sync := m[1]
	revokedAt := strings.Index(sync, "p.status === 'revoked'")
	if revokedAt < 0 {
		t.Fatal("syncPairedFromVisor no longer skips revoked pairs; /pair lists revoked records and the deleted conversation comes back on every load")
	}
	addedAt := strings.Index(sync, "this.pairedSet.add(pk)")
	if addedAt < 0 || revokedAt > addedAt {
		t.Error("the revoked check in syncPairedFromVisor must precede pairedSet.add — a revoked record is a tombstone, not a contact")
	}

	// deleteChat: a failed unpair leaves a live pair that resurrects the
	// conversation on the next load; the user must hear about it rather than
	// a console.debug line. Same body-extraction anchor as the test above:
	// from the declaration to the blank line + comment that follows it.
	body := deleteChatBody(t, page)
	unpairAt := strings.Index(body, "`/pair/${pk}`")
	if unpairAt < 0 {
		t.Fatal("deleteChat no longer revokes the pair of the conversation it deletes")
	}
	rest := body[unpairAt:]
	if !strings.Contains(rest, "showToast") || strings.Contains(rest, "console.debug('skychat: unpair failed") {
		t.Error("deleteChat's unpair failure is silent; a pair the visor still holds puts the conversation back on the next load and the user should be told")
	}
}

// deleteChatBody is what deleteChat does to a conversation: its own body plus
// _removeConversation, which it calls and blocking a request reuses.
func deleteChatBody(t *testing.T, page string) string {
	t.Helper()
	m := regexp.MustCompile(`(?s)\n\s+deleteChat\(\) \{\n(.*?)\n\s+\}\n\n\s+//`).FindStringSubmatch(page)
	if m == nil {
		t.Fatal("deleteChat() not found in index.html")
	}
	if !strings.Contains(m[1], "this._removeConversation(") {
		t.Fatal("deleteChat no longer removes the conversation through _removeConversation")
	}
	r := regexp.MustCompile(`(?s)\n\s+_removeConversation\(pk\) \{\n(.*?)\n\s+\}\n\n\s+//`).FindStringSubmatch(page)
	if r == nil {
		t.Fatal("_removeConversation(pk) not found in index.html")
	}
	return r[1] + "\n" + m[1]
}
