package dht

import (
	"testing"
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
)

func newTestMirror(t *testing.T) *RedisMirror {
	t.Helper()
	pk, sk := cipher.GenerateKeyPair()
	return &RedisMirror{
		salt:  "test",
		pk:    pk,
		sk:    sk,
		cache: make(map[cipher.PubKey]mirroredContent),
	}
}

func TestRedisMirror_SubjectsToPublish_EmptyCachePublishesAll(t *testing.T) {
	m := newTestMirror(t)
	pk1, _ := cipher.GenerateKeyPair()
	pk2, _ := cipher.GenerateKeyPair()

	got := m.subjectsToPublish([]cipher.PubKey{pk1, pk2}, 0xdeadbeef, time.Now())
	if len(got) != 2 {
		t.Fatalf("expected 2 subjects, got %d", len(got))
	}
}

func TestRedisMirror_SubjectsToPublish_SkipsMatchingHashWithinMaxAge(t *testing.T) {
	m := newTestMirror(t)
	pk, _ := cipher.GenerateKeyPair()
	now := time.Now()

	m.recordPublished(pk, 0x1234, now)
	got := m.subjectsToPublish([]cipher.PubKey{pk}, 0x1234, now.Add(time.Minute))
	if len(got) != 0 {
		t.Fatalf("expected 0 (skip), got %d", len(got))
	}
}

func TestRedisMirror_SubjectsToPublish_DoesNotSkipOnHashChange(t *testing.T) {
	m := newTestMirror(t)
	pk, _ := cipher.GenerateKeyPair()
	now := time.Now()

	m.recordPublished(pk, 0x1234, now)
	got := m.subjectsToPublish([]cipher.PubKey{pk}, 0x9999, now.Add(time.Second))
	if len(got) != 1 {
		t.Fatalf("expected 1 (re-publish on hash change), got %d", len(got))
	}
}

func TestRedisMirror_SubjectsToPublish_RefreshesAfterMaxAge(t *testing.T) {
	m := newTestMirror(t)
	pk, _ := cipher.GenerateKeyPair()
	now := time.Now()

	m.recordPublished(pk, 0x1234, now)
	got := m.subjectsToPublish([]cipher.PubKey{pk}, 0x1234, now.Add(contentCacheMaxAge+time.Second))
	if len(got) != 1 {
		t.Fatalf("expected 1 (refresh after max age), got %d", len(got))
	}
}

func TestRedisMirror_SubjectsToPublish_MixedBatchPartialSkip(t *testing.T) {
	m := newTestMirror(t)
	pkSkip, _ := cipher.GenerateKeyPair()
	pkFresh, _ := cipher.GenerateKeyPair()
	pkChanged, _ := cipher.GenerateKeyPair()
	now := time.Now()

	m.recordPublished(pkSkip, 0xAAAA, now)
	m.recordPublished(pkChanged, 0xBBBB, now)
	// pkFresh has no cache entry.

	got := m.subjectsToPublish([]cipher.PubKey{pkSkip, pkFresh, pkChanged}, 0xAAAA, now.Add(time.Second))
	if len(got) != 2 {
		t.Fatalf("expected 2 (skip=pkSkip, publish=pkFresh+pkChanged), got %d", len(got))
	}
	// Order preserved from input, minus the skipped entry.
	if got[0] != pkFresh || got[1] != pkChanged {
		t.Fatalf("unexpected order: %v", got)
	}
}

func TestRedisMirror_Delete_InvalidatesCache(t *testing.T) {
	m := newTestMirror(t)
	pk, _ := cipher.GenerateKeyPair()
	now := time.Now()

	m.recordPublished(pk, 0x1234, now)

	// Manually invalidate like Delete() does — can't call Delete() without backend.
	m.cacheMu.Lock()
	delete(m.cache, pk)
	m.cacheMu.Unlock()

	got := m.subjectsToPublish([]cipher.PubKey{pk}, 0x1234, now.Add(time.Second))
	if len(got) != 1 {
		t.Fatalf("expected 1 (cache cleared by delete), got %d", len(got))
	}
}

func TestRedisMirror_SubjectsToPublish_DoesNotMutateInput(t *testing.T) {
	m := newTestMirror(t)
	pk1, _ := cipher.GenerateKeyPair()
	pk2, _ := cipher.GenerateKeyPair()
	now := time.Now()
	m.recordPublished(pk1, 0xAAAA, now)

	input := []cipher.PubKey{pk1, pk2}
	origLen := len(input)
	origFirst := input[0]

	_ = m.subjectsToPublish(input, 0xAAAA, now.Add(time.Second))

	if len(input) != origLen || input[0] != origFirst {
		t.Fatalf("input slice was mutated")
	}
}
