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
	t.Setenv("SKYWIRE_TEST_UNSET", "1")
	AcquireInternalAppStart(ProcKey{1})
	done := make(chan struct{})
	go func() {
		UnsetInternalAppEnv([]string{"SKYWIRE_TEST_UNSET=1"})
		close(done)
	}()

	time.Sleep(50 * time.Millisecond)
	require.Equal(t, "1", os.Getenv("SKYWIRE_TEST_UNSET"), "removed during a start")

	ReleaseInternalAppStart(ProcKey{1})
	<-done
	_, set := os.LookupEnv("SKYWIRE_TEST_UNSET")
	require.False(t, set)
}
