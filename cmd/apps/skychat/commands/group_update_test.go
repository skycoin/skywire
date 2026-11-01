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
	prints := map[string]visorapi.GroupInfo{}

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
	if s := waitForString(t, sub, time.Second); !strings.Contains(s, `"channel":"group-notice"`) || !strings.Contains(s, `"event":"joined"`) || !strings.Contains(s, joiner.Hex()) {
		t.Fatalf("got %s, want a joined notice for the joiner", s)
	}

	scanGroupChanges(nil, prints)
	if len(prints) != 0 {
		t.Fatalf("a group that left the list is still remembered: %v", prints)
	}
}

// TestGroupNoticesSayEachChangeOnce: a ban is one notice, not also a departure
// and a lost admin role, and the rules changing is a notice of its own.
func TestGroupNoticesSayEachChangeOnce(t *testing.T) {
	owner, _ := cipher.GenerateKeyPair()
	a, _ := cipher.GenerateKeyPair()
	b, _ := cipher.GenerateKeyPair()
	old := visorapi.GroupInfo{Members: []cipher.PubKey{owner, a, b}, Admins: []cipher.PubKey{owner, a}, Muted: []cipher.PubKey{b}}
	now := visorapi.GroupInfo{Members: []cipher.PubKey{owner, b}, Admins: []cipher.PubKey{owner, b}, Muted: []cipher.PubKey{b}, Banned: []cipher.PubKey{a}, ReadOnly: true}

	got := groupNotices(old, now)
	want := []groupNotice{{"admin_added", b.Hex()}, {"banned", a.Hex()}, {"read_only_on", ""}}
	if len(got) != len(want) {
		t.Fatalf("notices = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("notices = %+v, want %+v", got, want)
		}
	}
	// b stays muted, so nothing is said about it; a kicked admin is one departure.
	kicked := visorapi.GroupInfo{Members: []cipher.PubKey{owner}, Admins: []cipher.PubKey{owner}, Muted: []cipher.PubKey{b}}
	if got := groupNotices(now, kicked); len(got) != 3 || got[0] != (groupNotice{"left", b.Hex()}) || got[1] != (groupNotice{"unbanned", a.Hex()}) || got[2] != (groupNotice{"read_only_off", ""}) {
		t.Fatalf("notices = %+v, want b left, a unbanned and read-only off", got)
	}
}
