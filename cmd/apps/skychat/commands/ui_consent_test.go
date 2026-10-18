// Package commands ui_consent_test.go
package commands

import (
	"regexp"
	"strings"
	"testing"
)

// pageMethod returns the body of one method of the page's app class.
func pageMethod(t *testing.T, page, name string) string {
	t.Helper()
	m := regexp.MustCompile(`(?s)\n      ` + regexp.QuoteMeta(name) + `\([^)]*\) \{\n(.*?)\n      \}\n`).FindStringSubmatch(page)
	if m == nil {
		t.Fatalf("%s not found in index.html", name)
	}
	return m[1]
}

// TestDeletingEndsConsent pins that deleting a chat or removing a contact
// ends the acceptance. It used to outlive both, so a peer once accepted and
// then deleted came back without the accept or block prompt.
func TestDeletingEndsConsent(t *testing.T) {
	page := embeddedPage(t)
	if b := pageMethod(t, page, "_unaccept"); !strings.Contains(b, "'/privacy/accept'") || !strings.Contains(b, "accepted: false") {
		t.Errorf("_unaccept does not post accepted: false to /privacy/accept:\n%s", b)
	}
	if b := pageMethod(t, page, "_removeConversation"); !strings.Contains(b, "this._unaccept(pk)") {
		t.Error("deleting a conversation keeps the peer accepted")
	}
	// A name lets a peer past approval as well, so it goes with the contact.
	b := pageMethod(t, page, "removeContact")
	if !strings.Contains(b, "this._unaccept(key)") || !strings.Contains(b, "this.setPeerName(key, '')") {
		t.Error("removing a contact keeps the peer accepted or named")
	}
}

// TestAcceptedPairIsAContact pins that an accepted pairing puts the peer in
// the address book on both sides: the accepter on accept, the initiator
// when its pair poll first sees the pair active.
func TestAcceptedPairIsAContact(t *testing.T) {
	page := embeddedPage(t)
	if !strings.Contains(pageMethod(t, page, "acceptInvite"), "this.addContact(pk)") {
		t.Error("accepting an invite does not add the peer to the address book")
	}
	for _, name := range []string{"syncPairedFromVisor", "_pollPairEpochs"} {
		if !strings.Contains(pageMethod(t, page, name), "this._contactFromPair(") {
			t.Errorf("%s does not add the paired peer to the address book", name)
		}
	}
}
