package restart

import (
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/SkycoinProject/skycoin/src/util/logging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCaptureContext(t *testing.T) {
	cc := CaptureContext()

	require.Equal(t, DefaultCheckDelay, cc.checkDelay)
	require.Equal(t, os.Args, cc.cmd.Args)
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
	cc := CaptureContext()
	assert.NotZero(t, len(cc.cmd.Args))

	t.Run("executable started", func(t *testing.T) {
		cmd := "touch"
		path := "/tmp/test_restart"
		cc.cmd = exec.Command(cmd, path) // nolint:gosec
		cc.appendDelay = false

		assert.NoError(t, cc.Start())
		assert.NoError(t, os.Remove(path))
	})

	t.Run("bad args", func(t *testing.T) {
		cmd := "bad_command"
		cc.cmd = exec.Command(cmd) // nolint:gosec

		// TODO(nkryuchkov): Check if it works on Linux and Windows, if not then change the error text.
		assert.EqualError(t, cc.Start(), `exec: "bad_command": executable file not found in $PATH`)
	})

	t.Run("already restarting", func(t *testing.T) {
		cmd := "touch"
		path := "/tmp/test_restart"
		cc.cmd = exec.Command(cmd, path) // nolint:gosec
		cc.appendDelay = false

		ch := make(chan error, 1)
		go func() {
			ch <- cc.Start()
		}()

		assert.NoError(t, cc.Start())
		assert.Equal(t, ErrAlreadyStarting, <-ch)

		assert.NoError(t, os.Remove(path))
	})
}

func TestContext_SetCheckDelay(t *testing.T) {
	cc := CaptureContext()
	require.Equal(t, DefaultCheckDelay, cc.checkDelay)

	const oneSecond = 1 * time.Second

	cc.SetCheckDelay(oneSecond)
	require.Equal(t, oneSecond, cc.checkDelay)
}
