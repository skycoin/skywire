package restart

import (
	"os"
	"testing"
	"time"

	"github.com/SkycoinProject/skycoin/src/util/logging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCaptureContext(t *testing.T) {
	cc, err := CaptureContext()
	require.NoError(t, err)

	wd, err := os.Getwd()
	assert.NoError(t, err)

	require.Equal(t, wd, cc.workingDirectory)
	require.Equal(t, DefaultCheckDelay, cc.checkDelay)
	require.Equal(t, os.Args, cc.args)
	require.Nil(t, cc.log)
	require.True(t, cc.needsExit)
}

func TestContext_RegisterLogger(t *testing.T) {
	cc, err := CaptureContext()
	require.NoError(t, err)
	require.Nil(t, cc.log)

	logger := logging.MustGetLogger("test")
	cc.RegisterLogger(logger)
	require.Equal(t, logger, cc.log)
}

func TestContext_Restart(t *testing.T) {
	cc, err := CaptureContext()
	require.NoError(t, err)
	assert.NotZero(t, len(cc.args))

	cc.workingDirectory = ""
	cc.needsExit = false

	t.Run("executable started", func(t *testing.T) {
		cmd := "touch"
		path := "/tmp/test_restart"
		args := []string{cmd, path}
		cc.args = args

		assert.NoError(t, cc.Restart())
		assert.NoError(t, os.Remove(path))
	})

	t.Run("bad args", func(t *testing.T) {
		cmd := "bad_command"
		args := []string{cmd}
		cc.args = args

		// TODO(nkryuchkov): Check if it works on Linux and Windows, if not then change the error text.
		assert.EqualError(t, cc.Restart(), `exec: "bad_command": executable file not found in $PATH`)
	})

	t.Run("empty args", func(t *testing.T) {
		cc.args = nil

		assert.Equal(t, ErrMalformedArgs, cc.Restart())
	})

	t.Run("already restarting", func(t *testing.T) {
		cc.args = nil

		cmd := "touch"
		path := "/tmp/test_restart"
		args := []string{cmd, path}
		cc.args = args

		ch := make(chan error, 1)
		go func() {
			ch <- cc.Restart()
		}()

		assert.NoError(t, cc.Restart())
		assert.NoError(t, os.Remove(path))

		assert.Equal(t, ErrAlreadyRestarting, <-ch)
	})
}

func TestContext_SetCheckDelay(t *testing.T) {
	cc, err := CaptureContext()
	require.NoError(t, err)
	require.Equal(t, DefaultCheckDelay, cc.checkDelay)

	const oneSecond = 1 * time.Second

	cc.SetCheckDelay(oneSecond)
	require.Equal(t, oneSecond, cc.checkDelay)
}
