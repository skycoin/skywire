// Package visorinit module_test.go: covers the concurrent dependency-graph
// module initializer. Everything here is in-process (hooks + goroutines), so
// no network or visor runtime is involved.
package visorinit

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/logging"
)

func testML() *logging.MasterLogger { return logging.NewMasterLogger() }

// recorder tracks the order in which module hooks run.
type recorder struct {
	mu     sync.Mutex
	order  []string
	counts map[string]int
}

func newRecorder() *recorder { return &recorder{counts: map[string]int{}} }

func (r *recorder) hook(name string) Hook {
	return func(_ context.Context, _ *logging.Logger) error {
		r.mu.Lock()
		defer r.mu.Unlock()
		r.order = append(r.order, name)
		r.counts[name]++
		return nil
	}
}

func (r *recorder) ranBefore(t *testing.T, a, b string) {
	t.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()
	ai, bi := -1, -1
	for i, n := range r.order {
		if n == a && ai == -1 {
			ai = i
		}
		if n == b {
			bi = i
		}
	}
	require.NotEqual(t, -1, ai, "%s never ran", a)
	require.NotEqual(t, -1, bi, "%s never ran", b)
	require.Less(t, ai, bi, "%s should run before %s", a, b)
}

func TestInitConcurrent_SingleModule(t *testing.T) {
	rec := newRecorder()
	m := MakeModule("solo", rec.hook("solo"), testML())

	m.InitConcurrent(context.Background())
	require.NoError(t, m.Wait(context.Background()))
	require.Equal(t, 1, rec.counts["solo"])
}

func TestInitConcurrent_DepsRunFirst(t *testing.T) {
	rec := newRecorder()
	ml := testML()
	a := MakeModule("A", rec.hook("A"), ml)
	b := MakeModule("B", rec.hook("B"), ml)
	c := MakeModule("C", rec.hook("C"), ml, &a, &b)

	c.InitConcurrent(context.Background())
	require.NoError(t, c.Wait(context.Background()))

	// Both deps must complete before the parent's own init runs.
	rec.ranBefore(t, "A", "C")
	rec.ranBefore(t, "B", "C")
}

func TestInitConcurrent_DiamondInitsSharedDepOnce(t *testing.T) {
	rec := newRecorder()
	ml := testML()
	// D -> {B, C} -> A. A is shared and must init exactly once thanks to start().
	a := MakeModule("A", rec.hook("A"), ml)
	b := MakeModule("B", rec.hook("B"), ml, &a)
	c := MakeModule("C", rec.hook("C"), ml, &a)
	d := MakeModule("D", rec.hook("D"), ml, &b, &c)

	d.InitConcurrent(context.Background())
	require.NoError(t, d.Wait(context.Background()))

	require.Equal(t, 1, rec.counts["A"], "shared dependency must init once")
	rec.ranBefore(t, "A", "B")
	rec.ranBefore(t, "A", "C")
	rec.ranBefore(t, "B", "D")
	rec.ranBefore(t, "C", "D")
}

func TestInitConcurrent_DepErrorPropagates(t *testing.T) {
	ml := testML()
	boom := errors.New("dep failed")
	failing := MakeModule("failing", func(_ context.Context, _ *logging.Logger) error {
		return boom
	}, ml)

	var ranParent bool
	parent := MakeModule("parent", func(_ context.Context, _ *logging.Logger) error {
		ranParent = true
		return nil
	}, ml, &failing)

	parent.InitConcurrent(context.Background())
	err := parent.Wait(context.Background())
	require.Error(t, err)
	require.Contains(t, err.Error(), "dep failed")
	require.False(t, ranParent, "parent init must not run when a dependency errors")
}

func TestInitConcurrent_OwnInitError(t *testing.T) {
	ml := testML()
	m := MakeModule("self", func(_ context.Context, _ *logging.Logger) error {
		return errors.New("kaboom")
	}, ml)

	m.InitConcurrent(context.Background())
	err := m.Wait(context.Background())
	require.Error(t, err)
	require.Contains(t, err.Error(), "initializing module self")
	require.Contains(t, err.Error(), "kaboom")
}

func TestInitConcurrent_NilInitYieldsErrNoInit(t *testing.T) {
	ml := testML()
	m := MakeModule("noinit", nil, ml)

	m.InitConcurrent(context.Background())
	err := m.Wait(context.Background())
	require.ErrorIs(t, err, ErrNoInit)
}

func TestInitConcurrent_IdempotentAfterFinish(t *testing.T) {
	rec := newRecorder()
	m := MakeModule("once", rec.hook("once"), testML())

	m.InitConcurrent(context.Background())
	require.NoError(t, m.Wait(context.Background()))

	// A second InitConcurrent after completion is a no-op (start() returns false).
	m.InitConcurrent(context.Background())
	require.NoError(t, m.Wait(context.Background()))
	require.Equal(t, 1, rec.counts["once"], "init must not run twice")
}

func TestWait_ContextCanceled(t *testing.T) {
	m := MakeModule("never", DoNothing, testML())
	// Never call InitConcurrent → done is never closed.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := m.Wait(ctx)
	require.ErrorIs(t, err, context.Canceled)
}

func TestWait_ContextDeadline(t *testing.T) {
	m := MakeModule("never", DoNothing, testML())
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	err := m.Wait(ctx)
	require.Error(t, err)
}

func TestDoNothing(t *testing.T) {
	require.NoError(t, DoNothing(context.Background(), nil))
}
