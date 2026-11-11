//go:build !windows

package pty

import (
	"io"
	"net"
	"testing"
	"time"

	rpc "github.com/0magnet/gobrpc"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/logging"
)

// echoPty answers Read once release closes.
type echoPty struct{ release chan struct{} }

func (p echoPty) Read(_ *int, out *[]byte) error {
	<-p.release
	*out = []byte("late output")
	return nil
}

func (echoPty) Ping(_, _ *struct{}) error { return nil }

// TestPtyClientReadAfterClose closes a client while a Read waits on the host,
// then lets the host answer. Read must not touch the reply the RPC client
// decodes late.
// Run with -race.
func TestPtyClientReadAfterClose(t *testing.T) {
	// A real socket, not net.Pipe: the pipe's channels would order the reply
	// against Read for the race detector and hide the race.
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer lis.Close() //nolint:errcheck
	srv := rpc.NewServer()
	release := make(chan struct{})
	require.NoError(t, srv.RegisterName(PtyRPCName, echoPty{release}))
	go func() {
		if sConn, err := lis.Accept(); err == nil {
			srv.ServeConn(sConn)
		}
	}()
	cConn, err := net.Dial("tcp", lis.Addr().String())
	require.NoError(t, err)

	sc := &PtyClient{
		log:  logging.MustGetLogger("test"),
		rpcC: rpc.NewClient(cConn),
		done: make(chan struct{}),
	}
	type result struct {
		n   int
		err error
	}
	got := make(chan result, 1)
	go func() {
		n, err := sc.Read(make([]byte, 64))
		got <- result{n, err}
	}()
	// Usually the Read is in flight by now. If not, it fails the same way.
	time.Sleep(50 * time.Millisecond)
	sc.close()
	r := <-got
	require.ErrorIs(t, r.err, io.ErrClosedPipe)
	require.Zero(t, r.n)
	close(release)

	// Any socket I/O here would order the late reply after Read, so wait
	// quietly for it to be decoded.
	time.Sleep(200 * time.Millisecond)
	require.NoError(t, sc.rpcC.Call(PtyRPCName+".Ping", &struct{}{}, &struct{}{}))
	require.NoError(t, sc.rpcC.Close())
}
