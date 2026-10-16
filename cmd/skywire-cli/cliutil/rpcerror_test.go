package cliutil

import (
	"errors"
	"strings"
	"testing"

	rpc "github.com/0magnet/gobrpc"
)

// A refusal from the visor is shown as sent. Only a failed call is explained
// as the visor being unreachable.
func TestRPCError(t *testing.T) {
	refused := rpc.ServerError("skyenv is written by `skywire autoconfig`, not by config set")
	if got := rpcError(refused).Error(); got != string(refused) {
		t.Errorf("refusal shown as %q", got)
	}
	for _, err := range []error{rpc.ErrShutdown, errors.New("dial tcp: connection refused"), rpc.ServerError("rpc: can't find method RPC.Nope")} {
		if got := rpcError(err).Error(); !strings.Contains(got, "is skywire running?") {
			t.Errorf("%v shown as %q", err, got)
		}
	}
}
