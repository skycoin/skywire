package api

import (
	"testing"
	"time"

	kcp "github.com/0magnet/kcp-go/v5"
	"github.com/stretchr/testify/require"
)

// A peer that stops acking fills the KCP send window. Writes to it must
// fail at the deadline instead of blocking the caller forever.
func TestWriteSUDPHGivesUpOnSilentPeer(t *testing.T) {
	old := sudphWriteTimeout
	sudphWriteTimeout = 200 * time.Millisecond
	t.Cleanup(func() { sudphWriteTimeout = old })

	l, err := kcp.Listen("127.0.0.1:0")
	require.NoError(t, err)
	defer l.Close() //nolint:errcheck

	c, err := kcp.Dial(l.Addr().String())
	require.NoError(t, err)
	_, err = c.Write([]byte("hello"))
	require.NoError(t, err)
	s, err := l.Accept()
	require.NoError(t, err)
	defer s.Close() //nolint:errcheck
	require.NoError(t, c.Close())

	done := make(chan error, 1)
	go func() {
		msg := make([]byte, 200)
		for {
			if err := writeSUDPH(s, msg); err != nil {
				done <- err
				return
			}
		}
	}()
	select {
	case err := <-done:
		require.Error(t, err)
	case <-time.After(10 * time.Second):
		t.Fatal("write to a silent peer blocked past its deadline")
	}
}
