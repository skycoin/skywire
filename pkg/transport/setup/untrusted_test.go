package setup

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/dmsg/dmsgtest"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/skyenv"
)

// servedHook counts the streams the listener starts serving RPC on.
type servedHook struct{ served atomic.Int64 }

func (h *servedHook) Levels() []logging.Level { return logging.AllLevels }
func (h *servedHook) Fire(e *logging.Entry) error {
	if e.Message == "Serving rpc" {
		h.served.Add(1)
	}
	return nil
}

// TestServeSkipsUntrustedStream dials the setup port from a key that is not
// a trusted transport setup node. Its stream must be closed and not served.
func TestServeSkipsUntrustedStream(t *testing.T) {
	env := dmsgtest.NewEnv(t, dmsgtest.DefaultTimeout)
	require.NoError(t, env.Startup(dmsgtest.DefaultTimeout, 1, 2, nil))
	t.Cleanup(env.Shutdown)
	dcA, dcB := env.AllClients()[0], env.AllClients()[1]

	ml := logging.NewMasterLogger()
	ml.SetLevel(logging.DebugLevel)
	hook := &servedHook{}
	ml.AddHook(hook)

	trusted, _ := cipher.GenerateKeyPair()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	tl, err := NewTransportListener(ctx, dcB.LocalPK(), []cipher.PubKey{trusted}, dcB, newTestManager(t), ml)
	require.NoError(t, err)
	done := make(chan struct{})
	go func() { defer close(done); tl.Serve(ctx) }()

	s, err := dcA.DialStream(ctx, dmsg.Addr{PK: dcB.LocalPK(), Port: skyenv.DmsgTransportSetupPort})
	require.NoError(t, err)
	defer s.Close()                                        //nolint:errcheck
	_ = s.SetReadDeadline(time.Now().Add(2 * time.Second)) //nolint:errcheck
	_, _ = s.Read(make([]byte, 1))                         //nolint:errcheck

	require.Zero(t, hook.served.Load(), "served RPC on an untrusted stream")
	cancel()
	<-done
}
