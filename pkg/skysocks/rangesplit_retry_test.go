package skysocks

import (
	"errors"
	"fmt"
	"testing"
	"time"
)

// TestRetryWithBudget_RecoversAfterTransientFailure pins the churn-resilience: a
// chunk that fails a few times (all-tunnels-down during a --tunnels rotation)
// then succeeds must be recovered, not abandoned.
func TestRetryWithBudget_RecoversAfterTransientFailure(t *testing.T) {
	calls := 0
	buf, err := retryWithBudget(func() ([]byte, error) {
		calls++
		if calls < 4 {
			return nil, errAllTunnelsDown // transient rotation window
		}
		return []byte("chunk"), nil
	}, 2*time.Second, time.Millisecond, 5*time.Millisecond)
	if err != nil {
		t.Fatalf("expected recovery, got err=%v after %d calls", err, calls)
	}
	if string(buf) != "chunk" {
		t.Fatalf("got %q, want %q", buf, "chunk")
	}
	if calls != 4 {
		t.Fatalf("expected 4 calls, got %d", calls)
	}
}

// TestRetryWithBudget_GivesUpAfterBudget: a chunk that never recovers returns the
// last error once the budget is spent (bounded, does not hang forever).
func TestRetryWithBudget_GivesUpAfterBudget(t *testing.T) {
	calls := 0
	start := time.Now()
	_, err := retryWithBudget(func() ([]byte, error) {
		calls++
		return nil, errAllTunnelsDown
	}, 40*time.Millisecond, time.Millisecond, 5*time.Millisecond)
	if !errors.Is(err, errAllTunnelsDown) {
		t.Fatalf("want errAllTunnelsDown, got %v", err)
	}
	if calls < rsChunkRetries {
		t.Fatalf("want at least %d attempts, got %d", rsChunkRetries, calls)
	}
	if time.Since(start) > time.Second {
		t.Fatal("retry loop ran far past its budget")
	}
}

// TestRetryWithBudget_MinAttemptsBeforeGivingUp: even with a zero budget the loop
// makes the minimum attempts (so a one-off blip still gets a couple of tries).
func TestRetryWithBudget_MinAttemptsBeforeGivingUp(t *testing.T) {
	calls := 0
	_, err := retryWithBudget(func() ([]byte, error) {
		calls++
		return nil, errAllTunnelsDown
	}, 0, time.Millisecond, time.Millisecond)
	if err == nil {
		t.Fatal("expected error")
	}
	if calls != rsChunkRetries {
		t.Fatalf("want exactly %d min attempts, got %d", rsChunkRetries, calls)
	}
}

// TestRetryWithBudget_SessionCloseRetriesFree: a tunnel that dies under the
// attempt must be refetched AT ONCE — no backoff sleep and no attempt charged to
// the chunk's budget — so a mid-download tunnel loss costs one round trip
// instead of pushing the chunk into the sequential rescue.
func TestRetryWithBudget_SessionCloseRetriesFree(t *testing.T) {
	calls := 0
	start := time.Now()
	buf, err := retryWithBudget(func() ([]byte, error) {
		calls++
		if calls < 4 {
			return nil, fmt.Errorf("%w: gone", errSessionClosed)
		}
		return []byte("chunk"), nil
	}, 0, 500*time.Millisecond, time.Second)
	if err != nil {
		t.Fatalf("expected recovery, got err=%v after %d calls", err, calls)
	}
	if string(buf) != "chunk" {
		t.Fatalf("got %q, want %q", buf, "chunk")
	}
	// A zero budget would have ended the loop at rsChunkRetries, and three
	// backoffs would have cost at least 1.5s. Neither may happen.
	if el := time.Since(start); el > 100*time.Millisecond {
		t.Fatalf("session-close retries backed off: took %v", el)
	}
}

// TestRetryWithBudget_SessionCloseIsBounded: the free retries are capped, so a
// tunnel set that keeps dying cannot spin the loop forever.
func TestRetryWithBudget_SessionCloseIsBounded(t *testing.T) {
	calls := 0
	_, err := retryWithBudget(func() ([]byte, error) {
		calls++
		return nil, fmt.Errorf("%w: gone", errSessionClosed)
	}, 0, time.Millisecond, time.Millisecond)
	if !errors.Is(err, errSessionClosed) {
		t.Fatalf("want errSessionClosed, got %v", err)
	}
	if calls != rsFreeRetries+rsChunkRetries {
		t.Fatalf("want %d attempts, got %d", rsFreeRetries+rsChunkRetries, calls)
	}
}
