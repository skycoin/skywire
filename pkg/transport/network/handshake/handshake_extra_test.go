// Package handshake — pkg/transport/network/handshake/handshake_extra_test.go:
// unit tests for the handshake error wrapper (Error / IsHandshakeError),
// the port-checker adapter (MakeF2PortChecker), and the responder-side
// rejection path (a failing CheckF2 makes the responder write frame3 with
// the error and both sides surface a wrapped handshake Error).
package handshake

import (
	"errors"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
)

func TestErrorAndIsHandshakeError(t *testing.T) {
	err := Error("boom")
	require.Equal(t, "handshake failed: boom", err.Error())
	require.True(t, IsHandshakeError(err))

	// A plain error is not a handshake Error.
	require.False(t, IsHandshakeError(errors.New("boom")))
	require.False(t, IsHandshakeError(nil))
}

func TestMakeF2PortChecker(t *testing.T) {
	// The adapter forwards the frame's destination port to the checker.
	var seen uint16
	checker := MakeF2PortChecker(func(port uint16) error {
		seen = port
		if port == 0 {
			return errors.New("port 0 refused")
		}
		return nil
	})

	require.NoError(t, checker(Frame2{DstAddr: dmsg.Addr{Port: 42}}))
	require.EqualValues(t, 42, seen)

	require.Error(t, checker(Frame2{DstAddr: dmsg.Addr{Port: 0}}))
}

// TestResponderRejection drives a full initiator/responder exchange where the
// responder's CheckF2 rejects the frame. The responder writes frame3 carrying
// the error; the initiator surfaces "handshake rejected", and both errors are
// wrapped as handshake Errors by the middleware.
func TestResponderRejection(t *testing.T) {
	initPK, initSK, err := cipher.GenerateDeterministicKeyPair([]byte("reject-init"))
	require.NoError(t, err)
	respPK, _, err := cipher.GenerateDeterministicKeyPair([]byte("reject-resp"))
	require.NoError(t, err)

	iAddr := dmsg.Addr{PK: initPK, Port: 10}
	rAddr := dmsg.Addr{PK: respPK, Port: 11}

	initC, respC := net.Pipe()
	deadline := time.Now().Add(Timeout)

	respErrCh := make(chan error, 1)
	go func() {
		respHS := ResponderHandshake(func(Frame2) error {
			return errors.New("policy: port closed")
		})
		_, _, e := respHS(respC, deadline)
		respErrCh <- e
	}()

	initHS := InitiatorHandshake(initSK, iAddr, rAddr)
	_, _, initErr := initHS(initC, deadline)
	require.Error(t, initErr)
	require.True(t, IsHandshakeError(initErr))

	require.Error(t, <-respErrCh)

	require.NoError(t, initC.Close())
	require.NoError(t, respC.Close())
}
