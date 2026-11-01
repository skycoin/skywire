package commands

import (
	"path/filepath"
	"testing"
	"time"
)

// TestDeleteOutboxKeepsADeleteUntilAcked: an unacknowledged delete comes due
// after a wait that grows, survives a restart, and goes once acknowledged or
// once it is too old to matter.
func TestDeleteOutboxKeepsADeleteUntilAcked(t *testing.T) {
	if appLog == nil {
		appLog = func(string, ...any) {}
	}
	orig := deletes
	t.Cleanup(func() { deletes = orig })
	path := filepath.Join(t.TempDir(), "deletes.json")
	openDeleteOutbox(path)
	now := time.Now()

	deletes.add("peer", "m1")
	if got := deletes.due(now); len(got) != 0 {
		t.Fatalf("due right after the first send: %v", got)
	}
	if got := deletes.due(now.Add(deleteRetryFirst + time.Second)); len(got["peer"]) != 1 || got["peer"][0] != "m1" {
		t.Fatalf("due after the first wait = %v, want m1", got)
	}
	if got := deletes.due(now.Add(deleteRetryFirst + 2*time.Second)); len(got) != 0 {
		t.Fatalf("due again at once: %v", got)
	}

	openDeleteOutbox(path)
	if got := deletes.due(now.Add(time.Second)); len(got["peer"]) != 1 {
		t.Fatalf("after a restart due = %v, want the unacknowledged m1", got)
	}

	deletes.acked("peer", "m1")
	if got := deletes.due(now.Add(time.Hour)); len(got) != 0 {
		t.Fatalf("an acknowledged delete came due: %v", got)
	}

	deletes.add("peer", "m2")
	if got := deletes.due(now.Add(deleteGiveUp + time.Hour)); len(got) != 0 {
		t.Fatalf("a delete past giving up came due: %v", got)
	}
}
