package treestore

import (
	"testing"
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
)

// waitPublished returns how long after start the publisher first published.
func waitPublished(t *testing.T, p *Publisher, after int64, start time.Time) time.Duration {
	t.Helper()
	for time.Since(start) < 10*time.Second {
		if p.lastPublishNs.Load() > after {
			return time.Since(start)
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("publisher never published")
	return 0
}

// TestPublisherLeadingEdge checks that a lone change on a quiet feed
// publishes at once, and that a change right after it still waits out the
// window, so the publish rate stays at one per window.
func TestPublisherLeadingEdge(t *testing.T) {
	const window = time.Second
	_, sk := cipher.GenerateKeyPair()
	p, err := New(nopNode(t), sk, Config{BatchWindow: window, LeadingEdge: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = p.Close() }) //nolint:errcheck

	start := time.Now()
	if err := p.Put("m/a", []byte("1")); err != nil {
		t.Fatal(err)
	}
	if d := waitPublished(t, p, 0, start); d > window/4 {
		t.Fatalf("first change published after %v, want well under the %v window", d, window)
	}

	last := p.lastPublishNs.Load()
	start = time.Now()
	if err := p.Put("m/b", []byte("2")); err != nil {
		t.Fatal(err)
	}
	if d := waitPublished(t, p, last, start); d < window/2 {
		t.Fatalf("second change published after %v, want it coalesced into the %v window", d, window)
	}
}

// TestPublisherTrailingWindow pins the default: without LeadingEdge every
// change waits the whole window.
func TestPublisherTrailingWindow(t *testing.T) {
	const window = time.Second
	_, sk := cipher.GenerateKeyPair()
	p, err := New(nopNode(t), sk, Config{BatchWindow: window})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = p.Close() }) //nolint:errcheck

	start := time.Now()
	if err := p.Put("m/a", []byte("1")); err != nil {
		t.Fatal(err)
	}
	if d := waitPublished(t, p, 0, start); d < window {
		t.Fatalf("published after %v, want the full %v window", d, window)
	}
}
