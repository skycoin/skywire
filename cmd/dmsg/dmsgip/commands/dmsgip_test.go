// Package commands cmd/dmsg/dmsgip/commands/dmsgip_test.go
//
// The RunE closure inlines everything and ends in a live dmsg connect + LookupIP
// (and Fatals on a bad --dmsgconf), so only its early validation is reachable as
// a unit test: an invalid --srv public key is rejected before any network I/O.
// The happy path and Execute are not unit-testable as-is.
package commands

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRunEInvalidServerKey verifies a malformed --srv public key is rejected
// before the command attempts to reach the dmsg network.
func TestRunEInvalidServerKey(t *testing.T) {
	// Drive the validation branch: a bad server key, no dmsghttp config file
	// (so the file-read/Fatal block is skipped), and an empty log level.
	dmsgServers = []string{"not-a-valid-public-key"}
	dmsgHTTPPath = ""
	logLvl = ""
	t.Cleanup(func() {
		dmsgServers = nil
		dmsgHTTPPath = ""
		logLvl = ""
	})

	errCh := make(chan error, 1)
	go func() { errCh <- RootCmd.RunE(RootCmd, nil) }()

	select {
	case err := <-errCh:
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to parse server public key")
	case <-time.After(2 * time.Second):
		t.Fatal("RunE did not return on an invalid --srv key; it must reject before connecting to dmsg")
	}
}
