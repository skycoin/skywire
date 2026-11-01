package commands

import (
	"strings"
	"testing"
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/visor/visorapi"
)

// TestScanGroupChangesAnnouncesARosterChange: a joiner or a promotion
// reaches the client as one group-update, and an unchanged group or the
// first sight of a group sends nothing.
func TestScanGroupChangesAnnouncesARosterChange(t *testing.T) {
	withHubAndPairing(t)
	sub, unsub := hub.subscribe()
	defer unsub()
	owner, _ := cipher.GenerateKeyPair()
	joiner, _ := cipher.GenerateKeyPair()
	g := visorapi.GroupInfo{ID: "g1", Name: "chan", Status: "active", CanPost: true, Members: []cipher.PubKey{owner}, Admins: []cipher.PubKey{owner}}
	prints := map[string]string{}

	scanGroupChanges([]visorapi.GroupInfo{g}, prints)
	scanGroupChanges([]visorapi.GroupInfo{g}, prints)
	select {
	case s := <-sub:
		t.Fatalf("sent %s for a group that did not change", s)
	case <-time.After(100 * time.Millisecond):
	}

	g.Members = append(g.Members, joiner)
	scanGroupChanges([]visorapi.GroupInfo{g}, prints)
	if s := waitForString(t, sub, time.Second); !strings.Contains(s, `"channel":"group-update"`) || !strings.Contains(s, `"group_id":"g1"`) {
		t.Fatalf("got %s, want a group-update for g1", s)
	}

	scanGroupChanges(nil, prints)
	if len(prints) != 0 {
		t.Fatalf("a group that left the list is still remembered: %v", prints)
	}
}
