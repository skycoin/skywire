//go:build !windows
// +build !windows

package restart

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/logging"
)

func TestCaptureContext(t *testing.T) {
	cc := CaptureContext()

	require.Equal(t, DefaultCheckDelay, cc.checkDelay)
	require.Equal(t, shellCommand, cc.cmd.Path)

	args := fmt.Sprintf("sleep 2; %s", strings.Join(os.Args, " "))
	expectedArgs := []string{shellCommand, commandFlag, args}
	require.Equal(t, expectedArgs, cc.cmd.Args)
	require.Equal(t, os.Stdout, cc.cmd.Stdout)
	require.Equal(t, os.Stdin, cc.cmd.Stdin)
	require.Equal(t, os.Stderr, cc.cmd.Stderr)
	require.Equal(t, os.Environ(), cc.cmd.Env)

	require.Nil(t, cc.log)
}

func TestContext_RegisterLogger(t *testing.T) {
	cc := CaptureContext()
	require.Nil(t, cc.log)

	logger := logging.MustGetLogger("test")
	cc.RegisterLogger(logger)
	require.Equal(t, logger, cc.log)
}

func TestContext_Start(t *testing.T) {
	t.Run("executable started", func(t *testing.T) {
		cc := CaptureContext()
		assert.NotZero(t, len(cc.cmd.Args))

		cmd := "touch"
		path := "/tmp/test_start"

		cc.cmd = exec.Command(cmd, path) // nolint:gosec

		assert.NoError(t, cc.start())
		assert.NoError(t, os.Remove(path))
	})

	t.Run("bad args", func(t *testing.T) {
		cc := CaptureContext()
		assert.NotZero(t, len(cc.cmd.Args))

		cmd := "bad_command"
		cc.cmd = exec.Command(cmd) // nolint:gosec

		// TODO: Add error text for Windows
		possibleErrors := []string{
			`exec: "bad_command": executable file not found in $PATH`,
			`exec: "bad_command": executable file not found in %PATH%`,
		}
		err := cc.start()
		require.NotNil(t, err)
		assert.Contains(t, possibleErrors, err.Error())
	})

	t.Run("already starting", func(t *testing.T) {
		cc := CaptureContext()
		assert.NotZero(t, len(cc.cmd.Args))

		cmd := "sleep"
		duration := "5"

		cc.cmd = exec.Command(cmd, duration) // nolint:gosec

		errCh := make(chan error, 1)
		go func() {
			errCh <- cc.start()
		}()

		err1 := cc.start()
		err2 := <-errCh
		errors := []error{err1, err2}

		assert.Contains(t, errors, ErrAlreadyStarted)
		assert.Contains(t, errors, nil)
	})
}

func TestContext_SetCheckDelay(t *testing.T) {
	cc := CaptureContext()
	require.Equal(t, DefaultCheckDelay, cc.checkDelay)

	cc.SetCheckDelay(time.Second)
	require.Equal(t, time.Second, cc.checkDelay)
}
