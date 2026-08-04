package visor

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/visor/visorconfig"
)

// TestSuspendSelectiveTeardown verifies the risky part of Suspend in
// isolation (no network): it must run every closeStack entry EXCEPT the
// kept local-RPC surface, in reverse (LIFO) order, retain only the kept
// entries afterward, reset the one-shot readiness channels, and be
// idempotent. Resume's full module re-init needs live validation and is
// not covered here.
func TestSuspendSelectiveTeardown(t *testing.T) {
	common := &visorconfig.Common{}
	common.SetLogger(logging.NewMasterLogger())
	v := &Visor{
		conf:     &visorconfig.V1{Common: common},
		initLock: new(sync.RWMutex),
	}

	// One-shot channels seeded as NewVisor would.
	v.dmsgHTTPReady = make(chan struct{})
	v.startupComplete = make(chan struct{})
	v.runtimeErrors = make(chan error)
	v.stun.ready = make(chan struct{})
	v.dmsgTracker.ready = make(chan struct{})
	oldStartup := v.startupComplete
	oldDmsgHTTP := v.dmsgHTTPReady
	oldStun := v.stun.ready
	oldTracker := v.dmsgTracker.ready
	oldRuntimeErrs := v.runtimeErrors

	var order []string
	mk := func(src string) closer {
		return closer{src: src, fn: func() error { order = append(order, src); return nil }}
	}
	// Interleave kept (cli.*) and network closers.
	v.closeStack = []closer{
		mk("cli.listener"),
		mk("transport.manager"),
		mk("cli.grpc"),
		mk("dmsg"),
		mk("router.serve"),
	}

	require.NoError(t, v.Suspend())

	// suspended flag set.
	suspended, err := v.IsSuspended()
	require.NoError(t, err)
	require.True(t, suspended)

	// Non-kept closers ran, in reverse (LIFO) order; kept ones did not.
	require.Equal(t, []string{"router.serve", "dmsg", "transport.manager"}, order)

	// Only the kept entries remain, in their original order.
	var remaining []string
	for _, cl := range v.closeStack {
		remaining = append(remaining, cl.src)
	}
	require.Equal(t, []string{"cli.listener", "cli.grpc"}, remaining)

	// One-shot readiness channels were replaced with fresh (open) ones.
	require.True(t, oldStartup != v.startupComplete, "startupComplete not reset")
	require.True(t, oldDmsgHTTP != v.dmsgHTTPReady, "dmsgHTTPReady not reset")
	require.True(t, oldStun != v.stun.ready, "stun.ready not reset")
	require.True(t, oldTracker != v.dmsgTracker.ready, "dmsgTracker.ready not reset")
	require.True(t, oldRuntimeErrs != v.runtimeErrors, "runtimeErrors not reset")

	// The reset channels must be open (not the closed ones from a torn-down run).
	select {
	case <-v.startupComplete:
		t.Fatal("startupComplete should be open after Suspend")
	default:
	}

	// Suspend is idempotent — a second call runs no closers.
	order = nil
	require.NoError(t, v.Suspend())
	require.Empty(t, order)
}
