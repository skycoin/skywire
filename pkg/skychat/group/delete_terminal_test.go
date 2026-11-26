package group

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/logging"
)

// openTerminalTestManager builds a Manager on a bbolt store at path, so the
// test can close and reopen the same file the way a visor restart does.
func openTerminalTestManager(t *testing.T, path string, myPK cipher.PubKey, sk cipher.SecKey) *Manager {
	t.Helper()
	st, err := OpenStore(path, sk)
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	return &Manager{
		store:              st,
		myPK:               myPK,
		log:                logging.MustGetLogger("group.delete-terminal-test"),
		sessions:           make(map[string]*Session),
		leaving:            make(map[string]context.CancelFunc),
		reconnectState:     make(map[string]*reconnectState),
		peerReconnectState: make(map[string]map[cipher.PubKey]*reconnectState),
		pendingJoinLast:    make(map[string]time.Time),
	}
}

// TestDeleteSurvivesConcurrentRosterSaves deletes a group while roster saves
// are in flight, then reopens the store. A save that read the record before
// the delete must not write the old status back over revoked.
func TestDeleteSurvivesConcurrentRosterSaves(t *testing.T) {
	me, sk := cipher.GenerateKeyPair()
	path := filepath.Join(t.TempDir(), "groups.db")
	members := make([]cipher.PubKey, 0, 64)
	members = append(members, me)
	for range 63 {
		members = append(members, mustPK(t))
	}
	for round := range 20 {
		m := openTerminalTestManager(t, path, me, sk)
		id := uuid.NewString()
		r := Record{ID: id, Name: "terminal", OwnerPK: me, Port: 60001, Mode: ModePublic,
			Kind: KindPublic, Members: members, Role: RoleOwner, Status: StatusActive}
		if err := m.store.Put(r); err != nil {
			t.Fatalf("Put: %v", err)
		}
		save := m.persistRoster(id)
		stop := make(chan struct{})
		var wg sync.WaitGroup
		for range 4 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for {
					select {
					case <-stop:
						return
					default:
						save(members, []cipher.PubKey{me})
					}
				}
			}()
		}
		time.Sleep(5 * time.Millisecond)
		if err := m.Delete(id); err != nil {
			t.Fatalf("Delete: %v", err)
		}
		time.Sleep(5 * time.Millisecond)
		close(stop)
		wg.Wait()
		if err := m.store.Close(); err != nil {
			t.Fatalf("Close: %v", err)
		}

		reopened := openTerminalTestManager(t, path, me, sk)
		got, ok, err := reopened.store.Get(id)
		_ = reopened.store.Close() //nolint:errcheck
		if err != nil || !ok {
			t.Fatalf("round %d: Get after reopen: ok %v err %v", round, ok, err)
		}
		if got.Status != StatusRevoked {
			t.Fatalf("round %d: status after delete and reopen = %q, want %q", round, got.Status, StatusRevoked)
		}
	}
}

// TestStaleWriteCannotUndoRevoke replays the exact interleaving: a writer
// reads the record, the group is deleted, and the writer then saves its copy.
func TestStaleWriteCannotUndoRevoke(t *testing.T) {
	me := mustPK(t)
	m := newStateTestManager(t, me)
	id := uuid.NewString()
	seedRecord(t, m, id, me, []cipher.PubKey{me}, StatusActive)

	stale, _, err := m.store.Get(id)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if err := m.Delete(id); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	stale.Members = append(stale.Members, mustPK(t))
	if err := m.store.Put(stale); err != nil {
		t.Fatalf("stale Put: %v", err)
	}
	if err := m.store.SetStatus(id, StatusActive); err != nil {
		t.Fatalf("SetStatus: %v", err)
	}
	got, _, _ := m.store.Get(id) //nolint:errcheck
	if got.Status != StatusRevoked {
		t.Fatalf("status after stale writes = %q, want %q", got.Status, StatusRevoked)
	}
}

// TestRosterSaveAfterLeaveKeepsLeft covers the member side: a roster save that
// lands after Leave must not bring the group back as active.
func TestRosterSaveAfterLeaveKeepsLeft(t *testing.T) {
	me, owner := mustPK(t), mustPK(t)
	m := newStateTestManager(t, me)
	id := uuid.NewString()
	seedRecord(t, m, id, owner, []cipher.PubKey{owner, me}, StatusActive)
	save := m.persistRoster(id)
	if err := m.terminate(id, StatusLeft); err != nil {
		t.Fatalf("terminate: %v", err)
	}
	save([]cipher.PubKey{owner, me, mustPK(t)}, []cipher.PubKey{owner})
	m.promoteIfRecovered(Record{ID: id, Status: StatusPending})
	got, _, _ := m.store.Get(id) //nolint:errcheck
	if got.Status != StatusLeft {
		t.Fatalf("status after late roster save = %q, want %q", got.Status, StatusLeft)
	}
	if len(got.Members) != 3 {
		t.Errorf("roster save should still record members, got %d", len(got.Members))
	}
}
