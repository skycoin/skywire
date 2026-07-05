// Package setup setup_test.go: unit tests for the transport-setup RPC
// gateway and listener. The gateway delegates to a *transport.Manager; the
// success paths (saving/listing/removing real transports) require a live
// transport network and are exercised by the integration suite. Here we
// cover the no-network branches: the unknown-network/not-found error paths,
// the empty listing, and the listener's connect-failure path.
package setup

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/transport"
	"github.com/skycoin/skywire/pkg/transport/network"
	types "github.com/skycoin/skywire/pkg/transport/types"
)

func newTestManager(t *testing.T) *transport.Manager {
	t.Helper()
	pk, _ := cipher.GenerateKeyPair()
	conf := &transport.ManagerConfig{PubKey: pk}
	tm, err := transport.NewManager(nil, nil, nil, conf, network.ClientFactory{})
	require.NoError(t, err)
	return tm
}

func newTestGateway(t *testing.T) (*TransportGateway, *transport.Manager) {
	t.Helper()
	tm := newTestManager(t)
	gw := &TransportGateway{tm: tm, log: logging.MustGetLogger("test")}
	return gw, tm
}

func TestTransportGateway_AddTransport_UnknownNetwork(t *testing.T) {
	gw, _ := newTestGateway(t)
	remote, _ := cipher.GenerateKeyPair()

	var res TransportResponse
	// No network client is registered, so the requested type is unknown and
	// SaveTransport fails before any dialing happens.
	err := gw.AddTransport(TransportRequest{RemotePK: remote, Type: types.Type("faketype")}, &res)
	require.Error(t, err)
	require.Equal(t, TransportResponse{}, res) // response untouched on error
}

func TestTransportGateway_RemoveTransport_NotFound(t *testing.T) {
	gw, _ := newTestGateway(t)

	var res BoolResponse
	err := gw.RemoveTransport(UUIDRequest{ID: uuid.New()}, &res)
	require.Error(t, err) // GetTransportByID → ErrNotFound
	require.False(t, res.Result)
}

func TestTransportGateway_RemoveTransport_IncorrectType(t *testing.T) {
	gw, tm := newTestGateway(t)

	// A user-labeled transport must NOT be removable via the setup RPC — this
	// is the security control that stops a setup node deleting transports a
	// local operator created manually. ErrIncorrectType is returned before any
	// deletion happens.
	id := uuid.New()
	mt := transport.NewManagedTransportForTest(nil)
	mt.Entry = transport.Entry{ID: id, Label: transport.LabelUser}
	tm.InjectTransportForTest(mt)

	var res BoolResponse
	err := gw.RemoveTransport(UUIDRequest{ID: id}, &res)
	require.ErrorIs(t, err, ErrIncorrectType)
	require.False(t, res.Result)
}

func TestTransportGateway_GetTransports_Empty(t *testing.T) {
	gw, _ := newTestGateway(t)

	var res []TransportResponse
	require.NoError(t, gw.GetTransports(struct{}{}, &res))
	require.Empty(t, res)
}

func TestErrIncorrectType(t *testing.T) {
	require.Error(t, ErrIncorrectType)
	require.Contains(t, ErrIncorrectType.Error(), "not created by skycoin")
}

func TestNewTransportListener_ConnectFailure(t *testing.T) {
	tm := newTestManager(t)
	pk, _ := cipher.GenerateKeyPair()
	ml := logging.NewMasterLogger()

	// A bare dmsg client never signals Ready(); a canceled context makes
	// NewTransportListener take the connect-failure branch.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	tl, err := NewTransportListener(ctx, pk, nil, &dmsg.Client{}, tm, ml)
	require.Error(t, err)
	require.Nil(t, tl)
	require.Contains(t, err.Error(), "failed to connect")
}
