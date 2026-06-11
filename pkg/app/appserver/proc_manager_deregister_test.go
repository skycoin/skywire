package appserver

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/app/appcommon"
)

// TestDeregister_UnknownKey_NoPanic is a regression test for a visor-crash
// nil-deref: Deregister with a key absent from procsByKey returned a nil
// *Proc, and the subsequent proc.appName dereference panicked — taking down
// the whole visor. Reachable via a duplicate/stale DeregisterApp during
// deploy churn. It must now return ErrNoSuchApp instead of panicking.
func TestDeregister_UnknownKey_NoPanic(t *testing.T) {
	m := &procManager{
		procsByKey: make(map[appcommon.ProcKey]*Proc),
	}

	require.NotPanics(t, func() {
		err := m.Deregister(appcommon.ProcKey{})
		require.ErrorIs(t, err, ErrNoSuchApp)
	})
}
