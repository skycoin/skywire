package visor

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/disc"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/logging"
)

// dmsg_ping must not hold the start until dmsg connects: the hypervisor
// depends on it, so a visor with no network never finished starting.
func TestInitDmsgPingDoesNotWaitForDmsg(t *testing.T) {
	pk, sk := cipher.GenerateKeyPair()
	dmsgC := dmsg.NewClient(pk, sk, disc.NewMock(0), &dmsg.Config{MinSessions: 1}) // never served, never ready
	t.Cleanup(func() { _ = dmsgC.Close() })                                        //nolint:errcheck
	v := &Visor{initLock: new(sync.RWMutex), dmsgC: dmsgC}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	initDone := make(chan error, 1)
	go func() { initDone <- initDmsgPing(ctx, v, logging.MustGetLogger("dmsg_ping_test")) }()
	select {
	case err := <-initDone:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("dmsg_ping waits for dmsg to connect")
	}

	// Closing before dmsg was ever ready.
	require.Len(t, v.closeStack, 1)
	require.NoError(t, v.closeStack[0].fn())
}
