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
	"github.com/skycoin/skywire/pkg/snet/arclient"
	"github.com/skycoin/skywire/pkg/snet/directtp"
	"github.com/skycoin/skywire/pkg/snet/directtp/pktable"
	"github.com/skycoin/skywire/pkg/snet/directtp/tptypes"
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

	arc                arclient.APIClient
	onNewNetworkTypeMu sync.Mutex
	onNewNetworkType   func(netType string)
	dmsgC              *dmsg.Client
}

// New creates a network from a config.
func New(conf Config, dmsgC *dmsg.Client, eb *appevent.Broadcaster, arc arclient.APIClient) (*Network, error) {
	clients := NetworkClients{
		Direct: make(map[string]directtp.Client),
	}

	if conf.NetworkConfigs.STCP != nil {
		conf := directtp.Config{
			Type:      tptypes.STCP,
			PK:        conf.PubKey,
			SK:        conf.SecKey,
			Table:     pktable.NewTable(conf.NetworkConfigs.STCP.PKTable),
			LocalAddr: conf.NetworkConfigs.STCP.LocalAddr,
			BeforeDialCallback: func(network, addr string) error {
				data := appevent.TCPDialData{RemoteNet: network, RemoteAddr: addr}
				event := appevent.NewEvent(appevent.TCPDial, data)
				_ = eb.Broadcast(context.Background(), event) //nolint:errcheck
				return nil
			},
		}
		clients.Direct[tptypes.STCP] = directtp.NewClient(conf)
	}

	if arc != nil {
		stcprConf := directtp.Config{
			Type:            tptypes.STCPR,
			PK:              conf.PubKey,
			SK:              conf.SecKey,
			AddressResolver: arc,
			BeforeDialCallback: func(network, addr string) error {
				data := appevent.TCPDialData{RemoteNet: network, RemoteAddr: addr}
				event := appevent.NewEvent(appevent.TCPDial, data)
				_ = eb.Broadcast(context.Background(), event) //nolint:errcheck
				return nil
			},
		}

		clients.Direct[tptypes.STCPR] = directtp.NewClient(stcprConf)

		sudphConf := directtp.Config{
			Type:            tptypes.SUDPH,
			PK:              conf.PubKey,
			SK:              conf.SecKey,
			AddressResolver: arc,
		}

		clients.Direct[tptypes.SUDPH] = directtp.NewClient(sudphConf)
	}

	return NewRaw(conf, clients, dmsgC, arc), nil
}

// NewRaw creates a network from a config and a dmsg client.
func NewRaw(conf Config, clients NetworkClients, dmsgC *dmsg.Client, arc arclient.APIClient) *Network {
	n := &Network{
		conf:    conf,
		nets:    make(map[string]struct{}),
		clients: clients,
		arc:     arc,
		dmsgC:   dmsgC,
	}

	if dmsgC != nil {
		n.addNetworkType(dmsgC.Type())
	}

	for k, v := range clients.Direct {
		if v != nil {
			n.addNetworkType(k)
		}
	}

	return n
}

// Conf gets network configuration.
func (n *Network) Conf() Config {
	return n.conf
}

// Init initiates server connections.
func (n *Network) Init() error {

	if n.conf.NetworkConfigs.STCP != nil {
		if client, ok := n.clients.Direct[tptypes.STCP]; ok && client != nil && n.conf.NetworkConfigs.STCP.LocalAddr != "" {
			if err := client.Serve(); err != nil {
				return fmt.Errorf("failed to initiate 'stcp': %w", err)
			}
		} else {
			log.Infof("No config found for stcp")
		}
	}

	if n.arc != nil {
		if client, ok := n.clients.Direct[tptypes.STCPR]; ok && client != nil {
			if err := client.Serve(); err != nil {
				return fmt.Errorf("failed to initiate 'stcpr': %w", err)
			}
		} else {
			log.Infof("No config found for stcpr")
		}

		if client, ok := n.clients.Direct[tptypes.SUDPH]; ok && client != nil {
			if err := client.Serve(); err != nil {
				return fmt.Errorf("failed to initiate 'sudph': %w", err)
			}
		} else {
			log.Infof("No config found for sudph")
		}
	}

	return nil
}

// OnNewNetworkType sets callback to be called when new network type is ready.
func (n *Network) OnNewNetworkType(callback func(netType string)) {
	n.onNewNetworkTypeMu.Lock()
	n.onNewNetworkType = callback
	n.onNewNetworkTypeMu.Unlock()
}

// IsNetworkReady checks whether network of type `netType` is ready.
func (n *Network) IsNetworkReady(netType string) bool {
	n.netsMu.Lock()
	_, ok := n.nets[netType]
	n.netsMu.Unlock()
	return ok
}

// Close closes underlying connections.
func (n *Network) Close() error {
	n.netsMu.Lock()
	defer n.netsMu.Unlock()

	wg := new(sync.WaitGroup)

	var directErrorsMu sync.Mutex
	directErrors := make(map[string]error)

	for k, v := range n.clients.Direct {
		if v != nil {
			wg.Add(1)
			go func() {
				err := v.Close()

				directErrorsMu.Lock()
				directErrors[k] = err
				directErrorsMu.Unlock()

				wg.Done()
			}()
		}
	}

	wg.Wait()

	for _, err := range directErrors {
		if err != nil {
			return err
		}
	}

	return nil
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
	switch network {
	case dmsg.Type:
		addr := dmsg.Addr{
			PK:   pk,
			Port: port,
		}

		conn, err := n.dmsgC.Dial(ctx, addr)
		if err != nil {
			return nil, fmt.Errorf("dmsg client dial %v: %w", addr, err)
		}

		return makeConn(conn, network), nil
	default:
		client, ok := n.clients.Direct[network]
		if !ok {
			return nil, ErrUnknownNetwork
		}

		conn, err := client.Dial(ctx, pk, port)
		if err != nil {
			return nil, fmt.Errorf("dial: %w", err)
		}

		log.Infof("Dialed %v, conn local address %q, remote address %q", network, conn.LocalAddr(), conn.RemoteAddr())
		return makeConn(conn, network), nil
	}
}

// Listen listens on the specified port.
func (n *Network) Listen(network string, port uint16) (*Listener, error) {
	switch network {
	case dmsg.Type:
		lis, err := n.dmsgC.Listen(port)
		if err != nil {
			return nil, err
		}

		return makeListener(lis, network), nil
	default:
		client, ok := n.clients.Direct[network]
		if !ok {
			return nil, ErrUnknownNetwork
		}

		lis, err := client.Listen(port)
		if err != nil {
			return nil, fmt.Errorf("listen: %w", err)
		}

		return makeListener(lis, network), nil
	}
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
