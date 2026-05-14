package cxds

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/skycoin/skycoin/src/cipher"

	"github.com/skycoin/skywire/pkg/cxo/data"
)

// TestRunBatch_DriveSetsArePersistedOnNilReturn confirms that a sequence of
// Set calls executed inside RunBatch are durable after fn returns nil — i.e.
// the surrounding tx commits — and that an outside Get from the same store
// can see them with rc=1.
func TestRunBatch_DriveSetsArePersistedOnNilReturn(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rb.db")
	ds, err := NewDriveCXDS(path)
	if err != nil {
		t.Fatalf("NewDriveCXDS: %v", err)
	}
	t.Cleanup(func() { _ = ds.Close() }) //nolint:errcheck

	keys := make([]cipher.SHA256, 8)
	if err := ds.RunBatch(func(scoped data.CXDS) error {
		for i := range keys {
			val := []byte{byte(i), 0xAA}
			keys[i] = cipher.SumSHA256(val)
			rc, err := scoped.Set(keys[i], val, 1)
			if err != nil {
				return err
			}
			if rc != 1 {
				t.Errorf("batch Set rc = %d; want 1 (fresh insert)", rc)
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("RunBatch: %v", err)
	}

	for i, k := range keys {
		val, rc, err := ds.Get(k, 0)
		if err != nil {
			t.Errorf("Get[%d]: %v", i, err)
			continue
		}
		if rc != 1 {
			t.Errorf("Get[%d] rc = %d; want 1", i, rc)
		}
		if len(val) != 2 || val[0] != byte(i) || val[1] != 0xAA {
			t.Errorf("Get[%d] val = %v; want [%d 0xAA]", i, val, i)
		}
	}
}

// TestRunBatch_DriveRollsBackOnError confirms that a non-nil return from fn
// drops every Set the batch performed — bbolt rolls back the whole tx, so
// none of the keys end up visible after RunBatch returns.
func TestRunBatch_DriveRollsBackOnError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rb.db")
	ds, err := NewDriveCXDS(path)
	if err != nil {
		t.Fatalf("NewDriveCXDS: %v", err)
	}
	t.Cleanup(func() { _ = ds.Close() }) //nolint:errcheck

	sentinel := errors.New("intentional rollback")
	keys := make([]cipher.SHA256, 4)
	rbErr := ds.RunBatch(func(scoped data.CXDS) error {
		for i := range keys {
			val := []byte{byte(i)}
			keys[i] = cipher.SumSHA256(val)
			if _, err := scoped.Set(keys[i], val, 1); err != nil {
				return err
			}
		}
		return sentinel
	})
	if !errors.Is(rbErr, sentinel) {
		t.Fatalf("RunBatch err = %v; want sentinel", rbErr)
	}

	for i, k := range keys {
		if _, _, err := ds.Get(k, 0); !errors.Is(err, data.ErrNotFound) {
			t.Errorf("after rollback, Get[%d] err = %v; want ErrNotFound", i, err)
		}
	}
}

// TestRunBatch_DriveInteropWithUnbatchedSet confirms that an unbatched
// Set/Get/Inc executed against the underlying store sees writes a previous
// RunBatch made — i.e. the batch and per-op paths share the same bucket.
func TestRunBatch_DriveInteropWithUnbatchedSet(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rb.db")
	ds, err := NewDriveCXDS(path)
	if err != nil {
		t.Fatalf("NewDriveCXDS: %v", err)
	}
	t.Cleanup(func() { _ = ds.Close() }) //nolint:errcheck

	val := []byte("hello")
	key := cipher.SumSHA256(val)

	// Insert via batch.
	if err := ds.RunBatch(func(scoped data.CXDS) error {
		_, err := scoped.Set(key, val, 1)
		return err
	}); err != nil {
		t.Fatalf("RunBatch: %v", err)
	}

	// Bump rc via the unbatched path; should see the batch-inserted row.
	rc, err := ds.Inc(key, 1)
	if err != nil {
		t.Fatalf("unbatched Inc after batch: %v", err)
	}
	if rc != 2 {
		t.Errorf("rc after unbatched Inc = %d; want 2", rc)
	}

	// Now bump rc inside another batch and confirm the unbatched Get sees it.
	if err := ds.RunBatch(func(scoped data.CXDS) error {
		_, err := scoped.Inc(key, 3)
		return err
	}); err != nil {
		t.Fatalf("RunBatch (Inc): %v", err)
	}

	_, finalRC, err := ds.Get(key, 0)
	if err != nil {
		t.Fatalf("final Get: %v", err)
	}
	if finalRC != 5 {
		t.Errorf("final rc = %d; want 5 (1 init + 1 unbatched Inc + 3 batched Inc)", finalRC)
	}
}

// TestRunBatch_MemoryIsPassthrough confirms that on the in-memory CXDS,
// RunBatch invokes fn with the receiver — there's no tx to commit and
// writes are visible to outside Get without RunBatch having returned.
func TestRunBatch_MemoryIsPassthrough(t *testing.T) {
	m := NewMemoryCXDS()
	t.Cleanup(func() { _ = m.Close() }) //nolint:errcheck

	val := []byte("xyz")
	key := cipher.SumSHA256(val)

	if err := m.RunBatch(func(scoped data.CXDS) error {
		if _, err := scoped.Set(key, val, 1); err != nil {
			t.Fatalf("scoped.Set: %v", err)
		}
		// Inside the batch, an outside Get should already see the write —
		// memory has no tx isolation.
		if _, rc, err := m.Get(key, 0); err != nil || rc != 1 {
			t.Errorf("Get during batch: rc=%d err=%v; want rc=1 nil", rc, err)
		}
		return nil
	}); err != nil {
		t.Fatalf("RunBatch: %v", err)
	}

	if _, rc, err := m.Get(key, 0); err != nil || rc != 1 {
		t.Errorf("Get after batch: rc=%d err=%v; want rc=1 nil", rc, err)
	}
}
