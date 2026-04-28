package pairing

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	path := filepath.Join(t.TempDir(), "pairs.db")
	s, err := OpenStore(path)
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Logf("Store.Close: %v", err)
		}
	})
	return s
}

func sampleRecord(t *testing.T) Record {
	t.Helper()
	pk, _ := cipher.GenerateKeyPair()
	return Record{
		PeerPK:        pk,
		Port:          12345,
		Status:        StatusPending,
		EstablishedAt: time.Now().UTC().Truncate(time.Millisecond),
	}
}

func TestStorePutGet(t *testing.T) {
	s := newTestStore(t)
	r := sampleRecord(t)

	if err := s.Put(r); err != nil {
		t.Fatalf("Put: %v", err)
	}
	got, ok, err := s.Get(r.PeerPK)
	if err != nil || !ok {
		t.Fatalf("Get: ok=%v err=%v", ok, err)
	}
	if got.PeerPK != r.PeerPK || got.Port != r.Port || got.Status != r.Status {
		t.Errorf("roundtrip mismatch: got %+v want %+v", got, r)
	}
}

func TestStoreGetMissing(t *testing.T) {
	s := newTestStore(t)
	pk, _ := cipher.GenerateKeyPair()
	_, ok, err := s.Get(pk)
	if err != nil {
		t.Fatalf("Get(missing): err=%v", err)
	}
	if ok {
		t.Error("Get(missing) should return ok=false")
	}
}

func TestStoreList(t *testing.T) {
	s := newTestStore(t)
	for i := 0; i < 3; i++ {
		if err := s.Put(sampleRecord(t)); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Errorf("List len = %d, want 3", len(got))
	}
}

func TestStoreDelete(t *testing.T) {
	s := newTestStore(t)
	r := sampleRecord(t)
	if err := s.Put(r); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete(r.PeerPK); err != nil {
		t.Fatal(err)
	}
	_, ok, err := s.Get(r.PeerPK)
	if err != nil {
		t.Fatalf("Get after Delete: %v", err)
	}
	if ok {
		t.Error("record should be gone after Delete")
	}
}

func TestStoreSetStatus(t *testing.T) {
	s := newTestStore(t)
	r := sampleRecord(t)
	if err := s.Put(r); err != nil {
		t.Fatal(err)
	}
	if err := s.SetStatus(r.PeerPK, StatusActive); err != nil {
		t.Fatal(err)
	}
	got, _, err := s.Get(r.PeerPK)
	if err != nil {
		t.Fatalf("Get after SetStatus: %v", err)
	}
	if got.Status != StatusActive {
		t.Errorf("status = %s, want active", got.Status)
	}
	// Other fields preserved.
	if got.Port != r.Port {
		t.Errorf("Port changed: got %d want %d", got.Port, r.Port)
	}
}

func TestStoreSetStatusMissing(t *testing.T) {
	s := newTestStore(t)
	pk, _ := cipher.GenerateKeyPair()
	if err := s.SetStatus(pk, StatusActive); err == nil {
		t.Fatal("SetStatus on missing record should error")
	}
}

func TestStoreMarkMessage(t *testing.T) {
	s := newTestStore(t)
	r := sampleRecord(t)
	if err := s.Put(r); err != nil {
		t.Fatal(err)
	}
	ts := time.Now().UTC().Truncate(time.Millisecond)
	if err := s.MarkMessage(r.PeerPK, ts); err != nil {
		t.Fatal(err)
	}
	got, _, err := s.Get(r.PeerPK)
	if err != nil {
		t.Fatalf("Get after MarkMessage: %v", err)
	}
	if !got.LastMessageAt.Equal(ts) {
		t.Errorf("LastMessageAt = %v, want %v", got.LastMessageAt, ts)
	}
}

func TestStorePersistence(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pairs.db")

	r := sampleRecord(t)
	s1, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := s1.Put(r); err != nil {
		t.Fatal(err)
	}
	if err := s1.Close(); err != nil {
		t.Fatal(err)
	}

	s2, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s2.Close() }() //nolint:errcheck

	got, ok, err := s2.Get(r.PeerPK)
	if err != nil || !ok {
		t.Fatalf("after reopen: ok=%v err=%v", ok, err)
	}
	if got.PeerPK != r.PeerPK || got.Status != r.Status {
		t.Errorf("roundtrip after reopen wrong: %+v", got)
	}
}
