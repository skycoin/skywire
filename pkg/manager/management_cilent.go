// Package manager pkg/manager/management_client.go
package manager

import (
	"errors"
	"sync"

	"github.com/skycoin/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire-utilities/pkg/logging"
	"github.com/skycoin/skywire/pkg/app/appnet"
	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/skyenv"
	"github.com/skycoin/skywire/pkg/visor/visorconfig"
)

// ManagementClient manages the connections of the Auth-Manager to remote ManagerServer's
type ManagementClient struct {
	managerConns map[cipher.PubKey]*RPCClient
	connMX       *sync.RWMutex
	log          *logging.Logger
}

// NewManagementClient create a new ManagementClient
func NewManagementClient(log *logging.Logger) *ManagementClient {
	mc := make(map[cipher.PubKey]*RPCClient)
	mv := &ManagementClient{
		log:          log,
		managerConns: mc,
	}
	return mv
}

// ErrAlreadyConnected is sent when a conn to the remote manager server is already available
var ErrAlreadyConnected = errors.New("already connected to the manager server")

// Connect attempts to connect the ManagerServer of the specified remote key
func (mc *ManagementClient) Connect(remotePK cipher.PubKey) error {
	client := mc.GetClient(remotePK)
	if client != nil {
		return ErrAlreadyConnected
	}

	connApp := appnet.Addr{
		Net:    appnet.TypeDmsg,
		PubKey: remotePK,
		Port:   routing.Port(skyenv.DmsgManagerRPCPort),
	}
	conn, err := appnet.Dial(connApp)
	if err != nil {
		return err
	}
	rc := newRPCClient(mc.log, conn, RPCPrefix, visorconfig.RPCTimeout)
	mc.addClient(remotePK, rc)
	return nil
}

// Disconnect attempts to disconnect from the ManagerServer of the specified remote key
func (mc *ManagementClient) Disconnect(remotePK cipher.PubKey) error {
	return mc.removeClient(remotePK)
}

func (mc *ManagementClient) addClient(remotePK cipher.PubKey, rc *RPCClient) {
	mc.connMX.Lock()
	defer mc.connMX.Unlock()
	mc.managerConns[remotePK] = rc
}

func (mc *ManagementClient) removeClient(remotePK cipher.PubKey) error {
	mc.connMX.Lock()
	defer mc.connMX.Unlock()
	rpcConn := mc.managerConns[remotePK]
	err := rpcConn.conn.Close()
	if err != nil {
		return err
	}
	delete(mc.managerConns, remotePK)

	return nil
}

// GetClient returns the RPCClient associated with the PK
func (mc *ManagementClient) GetClient(remotePK cipher.PubKey) *RPCClient {
	mc.connMX.Lock()
	defer mc.connMX.Unlock()
	return mc.managerConns[remotePK]
}
