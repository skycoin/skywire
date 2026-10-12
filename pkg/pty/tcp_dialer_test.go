package pty

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
)

// A Host reaches a standalone pty host's direct-TCP listener through
// NewTCPDialer under the identity it is given, which the listener's
// whitelist must hold.
func TestExecRemoteViaTCPDialer(t *testing.T) {
	srvPK, srvSK := cipher.GenerateKeyPair()
	allowedPK, allowedSK := cipher.GenerateKeyPair()
	otherPK, otherSK := cipher.GenerateKeyPair()

	wl := NewMemoryWhitelist()
	require.NoError(t, wl.Add(allowedPK))
	srv := NewHost(nil, wl)

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := lis.Addr().String()
	require.NoError(t, lis.Close())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go srv.ListenAndServeTCP(ctx, addr, srvPK, srvSK) //nolint:errcheck
	require.Eventually(t, func() bool {
		c, err := net.Dial("tcp", addr)
		if err == nil {
			_ = c.Close() //nolint:errcheck
		}
		return err == nil
	}, 5*time.Second, 20*time.Millisecond)

	client := NewHost(nil, NewMemoryWhitelist())
	req := &CommandExecReq{Name: "echo", Arg: []string{"hello"}, TimeoutMS: 5000}

	resp, err := client.ExecRemoteVia(ctx, NewTCPDialer(addr, allowedPK, allowedSK), srvPK, 2022, req)
	require.NoError(t, err)
	require.Equal(t, "hello\n", string(resp.Stdout))

	_, err = client.ExecRemoteVia(ctx, NewTCPDialer(addr, otherPK, otherSK), srvPK, 2023, req)
	require.Error(t, err, "a key off the whitelist must be refused")
}
