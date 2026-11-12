// Package appcommon pkg/app/appcommon/unset_env_test.go: the internal app env cleanup
package appcommon

import (
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// An exiting app's env cleanup waits for a start in progress, so it cannot
// remove the PROC_CONFIG a restarted instance has not read yet.
func TestUnsetInternalAppEnvWaitsForStart(t *testing.T) {
	t.Setenv("SKYWIRE_TEST_UNSET", "")
	SetInternalAppEnv(ProcKey{2}, []string{"SKYWIRE_TEST_UNSET=1"})
	AcquireInternalAppStart(ProcKey{1})
	done := make(chan struct{})
	go func() {
		UnsetInternalAppEnv(ProcKey{2}, []string{"SKYWIRE_TEST_UNSET=1"})
		close(done)
	}()

	time.Sleep(50 * time.Millisecond)
	require.Equal(t, "1", os.Getenv("SKYWIRE_TEST_UNSET"), "removed during a start")

	ReleaseInternalAppStart(ProcKey{1})
	<-done
	_, set := os.LookupEnv("SKYWIRE_TEST_UNSET")
	require.False(t, set)
}

// A restarted app sets the same vars as the instance it replaces. The old
// instance exiting afterwards must not remove them (DMSG_SRV_COUNT for the
// vpn-client, which then refused to start).
func TestUnsetInternalAppEnvKeepsNewerInstance(t *testing.T) {
	t.Setenv("SKYWIRE_TEST_RESTART", "")
	SetInternalAppEnv(ProcKey{3}, []string{"SKYWIRE_TEST_RESTART=old"})
	SetInternalAppEnv(ProcKey{4}, []string{"SKYWIRE_TEST_RESTART=new"})

	UnsetInternalAppEnv(ProcKey{3}, []string{"SKYWIRE_TEST_RESTART=old"})
	require.Equal(t, "new", os.Getenv("SKYWIRE_TEST_RESTART"))

	UnsetInternalAppEnv(ProcKey{4}, []string{"SKYWIRE_TEST_RESTART=new"})
	_, set := os.LookupEnv("SKYWIRE_TEST_RESTART")
	require.False(t, set)
}
