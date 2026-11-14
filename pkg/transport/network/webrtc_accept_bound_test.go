//go:build !tinygo && !(js && wasm)

package network

import (
	"context"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/dmsg/dmsgtest"
)

// TestWebRTCListenerBoundsAnswers opens many signaling streams whose answers
// never finish. The listener must run at most maxConcurrentWebRTCAnswers of
// them at once, since each answer is a whole PeerConnection.
func TestWebRTCListenerBoundsAnswers(t *testing.T) {
	env := dmsgtest.NewEnv(t, dmsgtest.DefaultTimeout)
	require.NoError(t, env.Startup(dmsgtest.DefaultTimeout, 1, 2, nil))
	t.Cleanup(env.Shutdown)
	dcA, dcB := env.AllClients()[0], env.AllClients()[1]

	var running, peak atomic.Int64
	release := make(chan struct{})
	orig := webrtcAnswer
	webrtcAnswer = func(ctx context.Context, _ io.ReadWriteCloser, _ []string, _ func(string)) (net.Conn, error) {
		n := running.Add(1)
		defer running.Add(-1)
		for {
			p := peak.Load()
			if n <= p || peak.CompareAndSwap(p, n) {
				break
			}
		}
		select {
		case <-release:
		case <-ctx.Done():
		}
		return nil, io.EOF
	}
	t.Cleanup(func() { webrtcAnswer = orig })

	lis, err := newWebRTCListener(dcB, nil, nil)
	require.NoError(t, err)
	defer lis.Close() //nolint:errcheck

	const streams = 3 * maxConcurrentWebRTCAnswers
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var wg sync.WaitGroup
	for range streams {
		wg.Go(func() {
			s, err := dcA.DialStream(ctx, dmsg.Addr{PK: dcB.LocalPK(), Port: webrtcSignalPort})
			if err == nil {
				<-ctx.Done()
				s.Close() //nolint:errcheck,gosec
			}
		})
	}

	require.Eventually(t, func() bool { return running.Load() == maxConcurrentWebRTCAnswers },
		5*time.Second, 10*time.Millisecond, "answers never reached the cap")
	time.Sleep(300 * time.Millisecond)
	require.LessOrEqual(t, peak.Load(), int64(maxConcurrentWebRTCAnswers))

	close(release)
	cancel()
	wg.Wait()
}
