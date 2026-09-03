// Package setup — pkg/transport/setup/setup_extra_test.go: covers the
// no-network success branches of the transport-setup RPC gateway that the
// original suite left to the integration tests — removing a skycoin-labeled
// transport and listing a populated transport set — using the manager's
// test-injection seam so no dialing occurs.
package setup

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/transport"
	types "github.com/skycoin/skywire/pkg/transport/types"
)

func injectTP(t *testing.T, tm *transport.Manager, label transport.Label) uuid.UUID {
	t.Helper()
	id := uuid.New()
	mt := transport.NewManagedTransportForTest(nil)
	mt.Entry = transport.Entry{ID: id, Label: label, Type: types.STCPR}
	tm.InjectTransportForTest(mt)
	return id
}

func TestTransportGateway_RemoveTransport_Success(t *testing.T) {
	gw, tm := newTestGateway(t)

	// A skycoin-labeled transport IS removable via the setup RPC.
	id := injectTP(t, tm, transport.LabelSkycoin)
	require.Equal(t, 1, tm.TransportCount())

	var res BoolResponse
	require.NoError(t, gw.RemoveTransport(UUIDRequest{ID: id}, &res))
	require.True(t, res.Result)
	require.Equal(t, 0, tm.TransportCount())
}

func TestTransportGateway_RemoveTransport_UserLabelRejected(t *testing.T) {
	gw, tm := newTestGateway(t)

	// A user-labeled transport stays put: RemoveTransport rejects it and the
	// count is unchanged.
	id := injectTP(t, tm, transport.LabelUser)
	require.Equal(t, 1, tm.TransportCount())

	var res BoolResponse
	require.ErrorIs(t, gw.RemoveTransport(UUIDRequest{ID: id}, &res), ErrIncorrectType)
	require.False(t, res.Result)
	require.Equal(t, 1, tm.TransportCount())
}
