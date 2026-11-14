package skyobject

import (
	"fmt"
	"testing"
	"time"

	"github.com/skycoin/skycoin/src/cipher"
)

func deltaKeys(n int, tag string) (ks []cipher.SHA256) {
	for i := range n {
		ks = append(ks, cipher.SumSHA256(fmt.Appendf(nil, "%s%d", tag, i)))
	}
	return
}

func TestDeltaStore(t *testing.T) {
	c := deltaTestContainer(t)
	defer c.Close() //nolint:errcheck,gosec

	pk, _ := cipher.GenerateKeyPair()
	a, b := deltaKeys(2, "a"), deltaKeys(2, "b")

	c.RecordDelta(pk, 1, 5, a)
	c.RecordDelta(pk, 1, 6, append([]cipher.SHA256{a[0]}, b...))

	keys, found := c.DeltaKeys(pk, 1, 5, 6, 10)
	if !found || len(keys) != 4 {
		t.Fatalf("keys %d found %v, want 4 without duplicates", len(keys), found)
	}
	if keys[0] != a[0] || keys[1] != b[0] {
		t.Fatal("the newest Root's keys must come first")
	}
	if keys, _ = c.DeltaKeys(pk, 1, 6, 6, 10); len(keys) != 3 {
		t.Fatalf("since 6: %d keys, want 3", len(keys))
	}
	if keys, _ = c.DeltaKeys(pk, 1, 5, 6, 2); len(keys) != 2 {
		t.Fatalf("limit 2: %d keys", len(keys))
	}
	if _, found = c.DeltaKeys(pk, 1, 5, 7, 10); found {
		t.Fatal("delta of seq 7 reported as known")
	}

	for seq := uint64(7); seq < 7+DeltaRoots; seq++ {
		c.RecordDelta(pk, 1, seq, deltaKeys(1, fmt.Sprint(seq)))
	}
	if _, found = c.DeltaKeys(pk, 1, 5, 6, 10); found {
		t.Fatal("more than DeltaRoots deltas kept")
	}

	big := deltaKeys(DeltaMaxKeys+10, "big")
	c.RecordDelta(pk, 2, 0, big)
	if keys, _ = c.DeltaKeys(pk, 2, 0, 0, 1<<20); len(keys) != DeltaMaxKeys {
		t.Fatalf("delta kept %d keys, want %d", len(keys), DeltaMaxKeys)
	}
}

func TestDeltaStoreTotal(t *testing.T) {
	c := deltaTestContainer(t)
	defer c.Close() //nolint:errcheck,gosec

	heads := DeltaTotalKeys/DeltaMaxKeys + 4
	pks := make([]cipher.PubKey, heads)
	for i := range pks {
		pks[i], _ = cipher.GenerateKeyPair()
		c.RecordDelta(pks[i], 1, 0, deltaKeys(DeltaMaxKeys, fmt.Sprint(i)))
	}
	if c.deltas.total > DeltaTotalKeys {
		t.Fatalf("total %d over %d", c.deltas.total, DeltaTotalKeys)
	}
	if _, found := c.DeltaKeys(pks[0], 1, 0, 0, 1); found {
		t.Fatal("the least recently recorded head was not evicted")
	}
	if _, found := c.DeltaKeys(pks[heads-1], 1, 0, 0, 1); !found {
		t.Fatal("the latest head was evicted")
	}
}

func TestWaitDeltaKeys(t *testing.T) {
	c := deltaTestContainer(t)
	defer c.Close() //nolint:errcheck,gosec

	pk, _ := cipher.GenerateKeyPair()
	go func() {
		time.Sleep(50 * time.Millisecond)
		c.RecordDelta(pk, 1, 3, deltaKeys(1, "x"))
	}()
	if _, found := c.WaitDeltaKeys(pk, 1, 0, 3, 10, 5*time.Second); !found {
		t.Fatal("recorded delta not seen by the waiter")
	}

	tp := time.Now()
	if _, found := c.WaitDeltaKeys(pk, 1, 0, 4, 10, 50*time.Millisecond); found {
		t.Fatal("missing delta found")
	}
	if time.Since(tp) < 50*time.Millisecond {
		t.Fatal("did not wait")
	}
}

func deltaTestContainer(t *testing.T) *Container {
	t.Helper()
	c, err := NewContainer(getTestConfig())
	if err != nil {
		t.Fatal(err)
	}
	return c
}
