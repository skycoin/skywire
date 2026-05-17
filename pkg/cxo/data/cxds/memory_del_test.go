// Package cxds pkg/cxo/data/cxds/memory_del_test.go: pins the
// memoryCXDS.Del invariant that Set+Del returns the underlying map
// to its original size. Pre-fix Del decremented amountAll/volumeAll
// but never executed `delete(m.kvs, key)`, leaking the value bytes
// indefinitely. The leak only surfaced in long-running production
// processes (dmsg-discovery, ~1 GB / 16 h on a host with active
// Publisher cleanup) because the bookkeeping counters reported by
// Amount/Volume *looked* right — only the actual heap was bleeding.
// This test asserts both the counter contract and the in-memory map
// contract so a regression that drops either line fails here.

package cxds

import (
	"crypto/sha256"
	"strconv"
	"testing"
)

func TestMemoryCXDS_Del_FreesMapEntry(t *testing.T) {
	ds := NewMemoryCXDS().(*memoryCXDS)
	defer ds.Close() //nolint:errcheck,gosec

	key := sha256.Sum256([]byte("a"))
	val := []byte("hello")

	if _, err := ds.Set(key, val, 1); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if got := len(ds.kvs); got != 1 {
		t.Fatalf("post-Set: map len = %d, want 1", got)
	}

	if err := ds.Del(key); err != nil {
		t.Fatalf("Del: %v", err)
	}

	// The contract the prod leak proved was missing: Del must
	// actually drop the entry from the backing map. Pre-fix the
	// counters would report 0 but the map still carried the bytes.
	if got := len(ds.kvs); got != 0 {
		t.Errorf("post-Del: map len = %d, want 0 (entry leak — bytes are still pinned in m.kvs)", got)
	}
	if all, used := ds.Amount(); all != 0 || used != 0 {
		t.Errorf("post-Del: Amount = (%d, %d), want (0, 0)", all, used)
	}
	if all, used := ds.Volume(); all != 0 || used != 0 {
		t.Errorf("post-Del: Volume = (%d, %d), want (0, 0)", all, used)
	}
}

func TestMemoryCXDS_Del_RepeatedSetDel_DoesNotGrow(t *testing.T) {
	// Reproduces the prod failure shape: Publisher.runCleanup nudges
	// per publish, each one walks the orphan set and calls Del for
	// every rc=0 hash. Pre-fix this loop would leak the bytes of
	// every orphan into m.kvs while reporting Amount=0. After N
	// publish cycles the heap holds N * payload bytes despite the
	// counters claiming the store is empty — exactly the dmsg-disc
	// pattern.
	ds := NewMemoryCXDS().(*memoryCXDS)
	defer ds.Close() //nolint:errcheck,gosec

	const cycles = 1000
	for i := 0; i < cycles; i++ {
		key := sha256.Sum256([]byte("k-" + strconv.Itoa(i)))
		val := []byte("payload-" + strconv.Itoa(i))
		if _, err := ds.Set(key, val, 1); err != nil {
			t.Fatalf("Set #%d: %v", i, err)
		}
		if err := ds.Del(key); err != nil {
			t.Fatalf("Del #%d: %v", i, err)
		}
	}

	if got := len(ds.kvs); got != 0 {
		t.Errorf("after %d Set+Del cycles: map len = %d, want 0 (heap leak — every Del leaves the entry behind)", cycles, got)
	}
	if all, used := ds.Amount(); all != 0 || used != 0 {
		t.Errorf("after %d cycles: Amount = (%d, %d), want (0, 0)", cycles, all, used)
	}
}

func TestMemoryCXDS_Del_AbsentKey_NoOp(t *testing.T) {
	// Del of a never-Set key must not mutate state or panic.
	// Pre-fix this was already correct (early `return` on
	// `ok == false`); pin it so the fix doesn't drop the guard.
	ds := NewMemoryCXDS().(*memoryCXDS)
	defer ds.Close() //nolint:errcheck,gosec

	key := sha256.Sum256([]byte("never-set"))
	if err := ds.Del(key); err != nil {
		t.Fatalf("Del of absent key: unexpected error %v", err)
	}
	if got := len(ds.kvs); got != 0 {
		t.Errorf("after Del of absent key: map len = %d, want 0", got)
	}
	if all, used := ds.Amount(); all != 0 || used != 0 {
		t.Errorf("after Del of absent key: Amount = (%d, %d), want (0, 0)", all, used)
	}
}
