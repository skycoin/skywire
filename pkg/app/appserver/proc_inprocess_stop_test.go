package appserver

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/app/appcommon"
	"github.com/skycoin/skywire/pkg/app/appevent"
)

// Stopping an in-process app returns only after its RunFunc has. Until then
// the app still serves its listeners and writes its files, which a visor
// stopping in place (the phone core) found still open after Stop.
func TestProcManager_StopWaitsForInProcessApp(t *testing.T) {
	m := newManager(t)
	defer func() { _ = m.Close() }() //nolint

	connected := make(chan struct{})
	returned := make(chan struct{})
	conf := internalConf("slowstop")
	conf.RunFunc = func(ctx context.Context, _ []string) error {
		defer close(returned)
		pc, err := appcommon.ProcConfigFromEnv()
		if err != nil {
			return err
		}
		conn, closers, err := appevent.DoReqHandshake(pc, nil)
		if err != nil {
			return err
		}
		defer conn.Close() //nolint:errcheck
		for _, c := range closers {
			defer c.Close() //nolint:errcheck
		}
		close(connected)
		<-ctx.Done()
		time.Sleep(300 * time.Millisecond) // closing its listeners, flushing its files
		return nil
	}

	_, err := m.Start(conf)
	require.NoError(t, err)
	select {
	case <-connected:
	case <-time.After(10 * time.Second):
		t.Fatal("the app never connected")
	}
	require.NoError(t, m.Stop("slowstop"))
	select {
	case <-returned:
	default:
		t.Fatal("Stop returned before the app's RunFunc did")
	}
}
