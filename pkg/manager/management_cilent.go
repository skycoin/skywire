// Package manager pkg/manager/management_client.go
package manager

import (
	"encoding/json"
	"errors"
	"sync"

	skycipher "github.com/skycoin/skycoin/src/cipher"
	"github.com/skycoin/skycoin/src/cipher/encrypt"

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
	localSK      cipher.SecKey
	cryptor      encrypt.ScryptChacha20poly1305
}

// NewManagementClient create a new ManagementClient
func NewManagementClient(log *logging.Logger, localSK cipher.SecKey) *ManagementClient {
	mc := make(map[cipher.PubKey]*RPCClient)
	mv := &ManagementClient{
		log:          log,
		managerConns: mc,
		localSK:      localSK,
		cryptor:      encrypt.DefaultScryptChacha20poly1305,
	}
	return mv
}

var (
	// ErrAlreadyConnected is sent when a conn to the remote manager server is already available
	ErrAlreadyConnected = errors.New("already connected to the manager server")

	// ErrNotConnected is sent when a conn to the remote manager server is not available
	ErrNotConnected = errors.New("not connected to the manager server")

	// ErrChalFailed is sent when a conn to the remote manager server is not available
	ErrChalFailed = errors.New("failed to solve the challenge sent by the manager server")
)

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
	err = mc.solveChallenge(remotePK, rc)
	if err != nil {
		return err
	}
	mc.addClient(remotePK, rc)
	return nil
}

func (mc *ManagementClient) solveChallenge(remotePK cipher.PubKey, rc *RPCClient) error {
	sharedSec, err := skycipher.ECDH(skycipher.PubKey(remotePK), skycipher.SecKey(mc.localSK))
	if err != nil {
		return err
	}

	resp, err := rc.Challenge()
	if err != nil {
		return err
	}

	byteArray, err := mc.cryptor.Decrypt(resp, sharedSec)
	if err != nil {
		return err
	}

	var con Connection
	err = json.Unmarshal(byteArray, &con)
	if err != nil {
		return err
	}

	conResp := Connection{
		Response: con.Challenge,
	}

	byteResp, err := json.Marshal(conResp)
	if err != nil {
		return err
	}

	encResp, err := mc.cryptor.Encrypt(byteResp, sharedSec)
	if err != nil {
		return err
	}

	isSolved, err := rc.Response(encResp)
	if err != nil {
		return err
	}
	if !isSolved {
		return ErrChalFailed
	}
	return nil
}

// Disconnect attempts to disconnect from the ManagerServer of the specified remote key
func (mc *ManagementClient) Disconnect(remotePK cipher.PubKey) error {
	return mc.removeClient(remotePK)
}

// List lists all the pk's of ongoing connections
func (mc *ManagementClient) List() cipher.PubKeys {
	return mc.getAllClientKeys()
}

func (mc *ManagementClient) getAllClientKeys() (pks cipher.PubKeys) {
	mc.connMX.Lock()
	defer mc.connMX.Unlock()
	for k := range mc.managerConns {
		pks = append(pks, k)
	}
	return pks
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
