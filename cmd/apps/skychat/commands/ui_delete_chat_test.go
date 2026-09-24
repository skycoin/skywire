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
	page := string(b)

	// The body of deleteChat: from its declaration to the next method.
	m := regexp.MustCompile(`(?s)\n\s+deleteChat\(\) \{\n(.*?)\n\s+\}\n\n\s+//`).FindStringSubmatch(page)
	if m == nil {
		t.Fatal("deleteChat() not found in index.html")
	}
	body := m[1]
	if !strings.Contains(body, "_forgetStoredConversation(pk)") {
		t.Error("deleteChat no longer erases the stored conversation; the visor's history will put it back on the next load")
	}
	if strings.Contains(body, "UI-local action") {
		t.Error("deleteChat still describes itself as UI-local; it is not")
	}

	m = regexp.MustCompile(`(?s)_forgetStoredConversation\(pk\) \{\n(.*?)\n\s+\}\n`).FindStringSubmatch(page)
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
