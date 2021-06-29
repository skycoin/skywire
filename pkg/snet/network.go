package snet

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"

	"github.com/skycoin/dmsg"
	"github.com/skycoin/dmsg/cipher"
	"github.com/skycoin/skycoin/src/util/logging"

	"github.com/skycoin/skywire/pkg/app/appevent"
	"github.com/skycoin/skywire/pkg/snet/directtp"
	"github.com/skycoin/skywire/pkg/snet/directtp/tptypes"
	"github.com/skycoin/skywire/pkg/transport/network/addrresolver"
)

var log = logging.MustGetLogger("snet")

var (
	// ErrUnknownNetwork occurs on attempt to dial an unknown network type.
	ErrUnknownNetwork = errors.New("unknown network type")
	knownNetworks     = map[string]struct{}{
		dmsg.Type:     {},
		tptypes.STCP:  {},
		tptypes.STCPR: {},
		tptypes.SUDPH: {},
	}
)

// IsKnownNetwork tells whether network type `netType` is known.
func IsKnownNetwork(netType string) bool {
	_, ok := knownNetworks[netType]
	return ok
}

// NetworkConfig is a common interface for network configs.
type NetworkConfig interface {
	Type() string
}

// STCPConfig defines config for STCP network.
type STCPConfig struct {
	PKTable   map[cipher.PubKey]string `json:"pk_table"`
	LocalAddr string                   `json:"local_address"`
}

// Type returns STCP type.
func (c *STCPConfig) Type() string {
	return tptypes.STCP
}

// Config represents a network configuration.
type Config struct {
	PubKey         cipher.PubKey
	SecKey         cipher.SecKey
	NetworkConfigs NetworkConfigs
}

// NetworkConfigs represents all network configs.
type NetworkConfigs struct {
	STCP *STCPConfig // The stcp service will not be started if nil.
}

// NetworkClients represents all network clients.
type NetworkClients struct {
	Direct map[string]directtp.Client
}

// Network represents a network between nodes in Skywire.
type Network struct {
	conf    Config
	netsMu  sync.RWMutex
	nets    map[string]struct{} // networks to be used with transports
	clients NetworkClients

	arc                addrresolver.APIClient
	onNewNetworkTypeMu sync.Mutex
	onNewNetworkType   func(netType string)
	dmsgC              *dmsg.Client
}

// New creates a network from a config.
func New(conf Config, dmsgC *dmsg.Client, eb *appevent.Broadcaster, arc addrresolver.APIClient, masterLogger *logging.MasterLogger) (*Network, error) {
	panic("remove")
}

// Conf gets network configuration.
func (n *Network) Conf() Config {
	return n.conf
}

// LocalPK returns local public key.
func (n *Network) LocalPK() cipher.PubKey { return n.conf.PubKey }

// LocalSK returns local secure key.
func (n *Network) LocalSK() cipher.SecKey { return n.conf.SecKey }

// TransportNetworks returns network types that are used for transports.
func (n *Network) TransportNetworks() []string {
	n.netsMu.RLock()
	networks := make([]string, 0, len(n.nets))
	for network := range n.nets {
		networks = append(networks, network)
	}
	n.netsMu.RUnlock()

	return networks
}

// STcp returns the underlying stcp.Client.
func (n *Network) STcp() (directtp.Client, bool) {
	return n.getClient(tptypes.STCP)
}

// STcpr returns the underlying stcpr.Client.
func (n *Network) STcpr() (directtp.Client, bool) {
	return n.getClient(tptypes.STCPR)
}

// SUdpH returns the underlying sudph.Client.
func (n *Network) SUdpH() (directtp.Client, bool) {
	return n.getClient(tptypes.SUDPH)
}

func (n *Network) getClient(tpType string) (directtp.Client, bool) {
	c, ok := n.clients.Direct[tpType]
	return c, ok
}

// Dial dials a visor by its public key and returns a connection.
func (n *Network) Dial(ctx context.Context, network string, pk cipher.PubKey, port uint16) (*Conn, error) {
	panic("remove")
}

// Listen listens on the specified port.
func (n *Network) Listen(network string, port uint16) (*Listener, error) {
	panic("remove")
}

func (n *Network) addNetworkType(netType string) {
	n.netsMu.Lock()
	defer n.netsMu.Unlock()

	if _, ok := n.nets[netType]; !ok {
		n.nets[netType] = struct{}{}
		n.onNewNetworkTypeMu.Lock()
		if n.onNewNetworkType != nil {
			n.onNewNetworkType(netType)
		}
		n.onNewNetworkTypeMu.Unlock()
	}
}

func disassembleAddr(addr net.Addr) (pk cipher.PubKey, port uint16) {
	strs := strings.Split(addr.String(), ":")
	if len(strs) != 2 {
		panic(fmt.Errorf("network.disassembleAddr: %v %s", "invalid addr", addr.String()))
	}

	if err := pk.Set(strs[0]); err != nil {
		panic(fmt.Errorf("network.disassembleAddr: %v %s", err, addr.String()))
	}

	if strs[1] != "~" {
		if _, err := fmt.Sscanf(strs[1], "%d", &port); err != nil {
			panic(fmt.Errorf("network.disassembleAddr: %w", err))
		}
	}

	return
}
