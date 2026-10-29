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

type echoPty struct{}

func (echoPty) Read(_ *int, out *[]byte) error {
	*out = []byte("late output")
	return nil
}

func (echoPty) Ping(_, _ *struct{}) error { return nil }

// TestPtyClientReadAfterClose reads from a closed client while the host still
// answers. Read must not touch the reply the RPC client may be decoding into.
// Run with -race.
func TestPtyClientReadAfterClose(t *testing.T) {
	// A real socket, not net.Pipe: the pipe's channels would order the reply
	// against Read for the race detector and hide the race.
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer lis.Close() //nolint:errcheck
	srv := rpc.NewServer()
	require.NoError(t, srv.RegisterName(PtyRPCName, echoPty{}))
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
	sc.close()
	n, err := sc.Read(make([]byte, 64))
	require.ErrorIs(t, err, io.ErrClosedPipe)
	require.Zero(t, n)

	// Any socket I/O here would order the late reply after Read, so wait
	// quietly for it to be decoded.
	time.Sleep(200 * time.Millisecond)
	require.NoError(t, sc.rpcC.Call(PtyRPCName+".Ping", &struct{}{}, &struct{}{}))
	require.NoError(t, sc.rpcC.Close())
}
