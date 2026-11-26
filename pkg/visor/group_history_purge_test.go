//go:build !js

package visor

import (
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/logging"
	skychatgroup "github.com/skycoin/skywire/pkg/skychat/group"
	"github.com/skycoin/skywire/pkg/skychat/history"
)

// newGroupHistoryPurgeVisor wires a Visor with a group store, a manager and a
// group history store, and seeds one owned group per status with history.
func newGroupHistoryPurgeVisor(t *testing.T, statuses ...skychatgroup.Status) (*Visor, *groupHistoryAdapter, []string) {
	t.Helper()
	pk, sk := cipher.GenerateKeyPair()
	dir := t.TempDir()
	store, err := skychatgroup.OpenStore(filepath.Join(dir, "groups.db"), sk)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() }) //nolint:errcheck
	log := logging.MustGetLogger("group-history-purge-test")
	mgr, err := skychatgroup.NewManager(skychatgroup.ManagerConfig{Store: store, DmsgC: new(dmsg.Client), MyPK: pk, MySK: sk, DataDir: dir, Logger: log})
	if err != nil {
		t.Fatal(err)
	}
	hs, err := history.NewBoltStore(filepath.Join(dir, "group-history.db"), groupHistoryLimits(nil))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = hs.Close() }) //nolint:errcheck
	adapter := &groupHistoryAdapter{store: hs, log: log}
	ids := make([]string, len(statuses))
	for i, st := range statuses {
		ids[i] = uuid.NewString()
		r := skychatgroup.Record{ID: ids[i], Name: "purge", OwnerPK: pk, Port: 60001, Mode: skychatgroup.ModePublic,
			Kind: skychatgroup.KindPublic, Members: []cipher.PubKey{pk}, Role: skychatgroup.RoleOwner, Status: st}
		if err := store.Put(r); err != nil {
			t.Fatal(err)
		}
		if err := adapter.AppendGroup(ids[i], pk.Hex(), "hello", time.Now().UTC(), true); err != nil {
			t.Fatal(err)
		}
	}
	v := &Visor{log: log, initLock: new(sync.RWMutex)}
	v.grouping.store = store
	v.grouping.manager = mgr
	v.grouping.history = adapter
	return v, adapter, ids
}

func storedGroups(t *testing.T, a *groupHistoryAdapter) []string {
	t.Helper()
	groups, err := a.Groups()
	if err != nil {
		t.Fatal(err)
	}
	return groups
}

func TestGroupDeleteAndLeaveDropHistory(t *testing.T) {
	v, adapter, ids := newGroupHistoryPurgeVisor(t, skychatgroup.StatusActive, skychatgroup.StatusActive, skychatgroup.StatusActive)
	if err := v.GroupDelete(ids[0]); err != nil {
		t.Fatalf("GroupDelete: %v", err)
	}
	if err := v.GroupLeave(ids[1]); err != nil {
		t.Fatalf("GroupLeave: %v", err)
	}
	got := storedGroups(t, adapter)
	if slices.Contains(got, ids[0]) || slices.Contains(got, ids[1]) || !slices.Contains(got, ids[2]) {
		t.Fatalf("stored groups = %v, want only %s", got, ids[2])
	}
	for i, want := range []skychatgroup.Status{skychatgroup.StatusRevoked, skychatgroup.StatusLeft} {
		r, ok, err := v.grouping.store.Get(ids[i])
		if err != nil || !ok || r.Status != want {
			t.Fatalf("record %s: ok %v err %v status %q, want the %q tombstone kept", ids[i], ok, err, r.Status, want)
		}
	}
}

func TestGroupHistoryPurgeEndedAtStartup(t *testing.T) {
	v, adapter, ids := newGroupHistoryPurgeVisor(t, skychatgroup.StatusRevoked, skychatgroup.StatusLeft, skychatgroup.StatusActive, skychatgroup.StatusRemoved)
	records, err := v.grouping.store.List()
	if err != nil {
		t.Fatal(err)
	}
	groupHistoryPurgeEnded(records, adapter, v.log)
	got := storedGroups(t, adapter)
	slices.Sort(got)
	want := []string{ids[2], ids[3]}
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Fatalf("stored groups after the startup pass = %v, want %v", got, want)
	}
}
