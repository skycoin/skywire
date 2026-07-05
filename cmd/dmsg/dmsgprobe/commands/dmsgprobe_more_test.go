// Package commands cmd/dmsg/dmsgprobe/commands/dmsgprobe_more_test.go
//
// Covers RunE's input-validation branches that return before any dmsg/tcp I/O:
// a malformed --via spec, an invalid destination public key, and an invalid
// port. The connection/probe paths beyond these guards reach the network and
// are not unit-testable as-is.
package commands

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
)

// runE invokes RootCmd.RunE with a timeout guard so a regression into the
// network path fails loudly instead of hanging.
func runE(t *testing.T, args []string) error {
	t.Helper()
	errCh := make(chan error, 1)
	go func() { errCh <- RootCmd.RunE(RootCmd, args) }()
	select {
	case err := <-errCh:
		return err
	case <-time.After(2 * time.Second):
		t.Fatal("RunE did not return before reaching the network")
		return nil
	}
}

func TestRunEValidation(t *testing.T) {
	t.Cleanup(func() { probeVia, probeServer, logLvl = "", "", "" })

	t.Run("bad --via spec", func(t *testing.T) {
		probeVia = "dmsg://not-tcp"
		defer func() { probeVia = "" }()
		err := runE(t, nil)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "tcp://")
	})

	t.Run("invalid destination pubkey", func(t *testing.T) {
		probeVia = ""
		err := runE(t, []string{"not-a-pubkey", "8080"})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "invalid public key")
	})

	t.Run("invalid port", func(t *testing.T) {
		probeVia = ""
		pk, _ := cipher.GenerateKeyPair()
		err := runE(t, []string{pk.Hex(), "not-a-port"})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "invalid port")
	})
}
