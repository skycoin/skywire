package snet

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/SkycoinProject/dmsg"
	"github.com/SkycoinProject/dmsg/cipher"
	"github.com/SkycoinProject/dmsg/disc"
	"github.com/SkycoinProject/skycoin/src/util/logging"

	"github.com/SkycoinProject/skywire-mainnet/pkg/app/appevent"
	"github.com/SkycoinProject/skywire-mainnet/pkg/snet/arclient"
	"github.com/SkycoinProject/skywire-mainnet/pkg/snet/directtransport"
)

var log = logging.MustGetLogger("snet")

// Default ports.
// TODO(evanlinjin): Define these properly. These are currently random.
const (
	SetupPort      = uint16(36)  // Listening port of a setup node.
	AwaitSetupPort = uint16(136) // Listening port of a visor for setup operations.
	TransportPort  = uint16(45)  // Listening port of a visor for incoming transports.
)

var (
	// ErrUnknownNetwork occurs on attempt to dial an unknown network type.
	ErrUnknownNetwork = errors.New("unknown network type")
)

// NetworkConfig is a common interface for network configs.
type NetworkConfig interface {
	Type() string
}

// DmsgConfig defines config for Dmsg network.
type DmsgConfig struct {
	Discovery     string `json:"discovery"`
	SessionsCount int    `json:"sessions_count"`
}

// Type returns DmsgType.
func (c *DmsgConfig) Type() string {
	return dmsg.Type
}

// STCPConfig defines config for STCP network.
type STCPConfig struct {
	PKTable   map[cipher.PubKey]string `json:"pk_table"`
	LocalAddr string                   `json:"local_address"`
}

// Type returns STCPType.
func (c *STCPConfig) Type() string {
	return directtransport.StcpType
}

// STCPRConfig defines config for STCPR network.
type STCPRConfig struct {
	AddressResolver string `json:"address_resolver"`
	LocalAddr       string `json:"local_address"`
}

// Type returns STCPRType.
func (c *STCPRConfig) Type() string {
	return directtransport.StcprType
}

// STCPHConfig defines config for STCPH network.
type STCPHConfig struct {
	AddressResolver string `json:"address_resolver"`
}

// Type returns STCPHType.
func (c *STCPHConfig) Type() string {
	return directtransport.StcphType
}

// SUDPConfig defines config for SUDP network.
type SUDPConfig struct {
	PKTable   map[cipher.PubKey]string `json:"pk_table"`
	LocalAddr string                   `json:"local_address"`
}

// Type returns STCPType.
func (c *SUDPConfig) Type() string {
	return directtransport.SudpType
}

// SUDPRConfig defines config for SUDPR network.
type SUDPRConfig struct {
	AddressResolver string `json:"address_resolver"`
	LocalAddr       string `json:"local_address"`
}

// Type returns STCPType.
func (c *SUDPRConfig) Type() string {
	return directtransport.SudprType
}

// SUDPHConfig defines config for SUDPH network.
type SUDPHConfig struct {
	AddressResolver string `json:"address_resolver"`
}

// Type returns STCPHType.
func (c *SUDPHConfig) Type() string {
	return directtransport.SudphType
}

// Config represents a network configuration.
type Config struct {
	PubKey         cipher.PubKey
	SecKey         cipher.SecKey
	NetworkConfigs NetworkConfigs
}

// NetworkConfigs represents all network configs.
type NetworkConfigs struct {
	Dmsg  *DmsgConfig  // The dmsg service will not be started if nil.
	STCP  *STCPConfig  // The stcp service will not be started if nil.
	STCPR *STCPRConfig // The stcpr service will not be started if nil.
	STCPH *STCPHConfig // The stcph service will not be started if nil.
	SUDP  *SUDPConfig  // The sudp service will not be started if nil.
	SUDPR *SUDPRConfig // The sudpr service will not be started if nil.
	SUDPH *SUDPHConfig // The sudph service will not be started if nil.
}

// NetworkClients represents all network clients.
type NetworkClients struct {
	DmsgC  *dmsg.Client
	Direct map[string]directtransport.Client
}

// Network represents a network between nodes in Skywire.
type Network struct {
	conf     Config
	networks []string // networks to be used with transports
	clients  NetworkClients
}

// New creates a network from a config.
func New(conf Config, eb *appevent.Broadcaster) (*Network, error) {
	clients := NetworkClients{
		Direct: make(map[string]directtransport.Client),
	}

	if conf.NetworkConfigs.Dmsg != nil {
		dmsgConf := &dmsg.Config{
			MinSessions: conf.NetworkConfigs.Dmsg.SessionsCount,
			Callbacks: &dmsg.ClientCallbacks{
				OnSessionDial: func(network, addr string) error {
					data := appevent.TCPDialData{RemoteNet: network, RemoteAddr: addr}
					event := appevent.NewEvent(appevent.TCPDial, data)
					_ = eb.Broadcast(context.Background(), event) //nolint:errcheck
					// @evanlinjin: An error is not returned here as this will cancel the session dial.
					return nil
				},
				OnSessionDisconnect: func(network, addr string, _ error) {
					data := appevent.TCPCloseData{RemoteNet: network, RemoteAddr: addr}
					event := appevent.NewEvent(appevent.TCPClose, data)
					_ = eb.Broadcast(context.Background(), event) //nolint:errcheck
				},
			},
		}
		clients.DmsgC = dmsg.NewClient(conf.PubKey, conf.SecKey, disc.NewHTTP(conf.NetworkConfigs.Dmsg.Discovery), dmsgConf)
		clients.DmsgC.SetLogger(logging.MustGetLogger("snet.dmsgC"))
	}

	// TODO(nkryuchkov): Generic code for clients below.
	if conf.NetworkConfigs.STCP != nil {
		conf := directtransport.ClientConfig{
			Type:      directtransport.StcpType,
			PK:        conf.PubKey,
			SK:        conf.SecKey,
			Table:     directtransport.NewTable(conf.NetworkConfigs.STCP.PKTable),
			LocalAddr: conf.NetworkConfigs.STCP.LocalAddr,
		}
		clients.Direct[directtransport.StcpType] = directtransport.NewClient(conf)
	}

	if conf.NetworkConfigs.STCPR != nil {
		ar, err := arclient.NewHTTP(conf.NetworkConfigs.STCPR.AddressResolver, conf.PubKey, conf.SecKey)
		if err != nil {
			return nil, err
		}

		conf := directtransport.ClientConfig{
			Type:            directtransport.StcprType,
			PK:              conf.PubKey,
			SK:              conf.SecKey,
			LocalAddr:       conf.NetworkConfigs.STCPR.LocalAddr,
			AddressResolver: ar,
		}

		clients.Direct[directtransport.StcprType] = directtransport.NewClient(conf)
	}

	if conf.NetworkConfigs.STCPH != nil {
		ar, err := arclient.NewHTTP(conf.NetworkConfigs.STCPH.AddressResolver, conf.PubKey, conf.SecKey)
		if err != nil {
			return nil, err
		}

		conf := directtransport.ClientConfig{
			Type:            directtransport.StcphType,
			PK:              conf.PubKey,
			SK:              conf.SecKey,
			AddressResolver: ar,
		}

		clients.Direct[directtransport.StcphType] = directtransport.NewClient(conf)
	}

	if conf.NetworkConfigs.SUDP != nil {
		conf := directtransport.ClientConfig{
			Type:      directtransport.SudpType,
			PK:        conf.PubKey,
			SK:        conf.SecKey,
			Table:     directtransport.NewTable(conf.NetworkConfigs.SUDP.PKTable),
			LocalAddr: conf.NetworkConfigs.SUDP.LocalAddr,
		}
		clients.Direct[directtransport.SudpType] = directtransport.NewClient(conf)
	}

	if conf.NetworkConfigs.SUDPR != nil {
		ar, err := arclient.NewHTTP(conf.NetworkConfigs.SUDPR.AddressResolver, conf.PubKey, conf.SecKey)
		if err != nil {
			return nil, err
		}

		conf := directtransport.ClientConfig{
			Type:            directtransport.SudprType,
			PK:              conf.PubKey,
			SK:              conf.SecKey,
			LocalAddr:       conf.NetworkConfigs.SUDPR.LocalAddr,
			AddressResolver: ar,
		}

		clients.Direct[directtransport.SudprType] = directtransport.NewClient(conf)
	}

	if conf.NetworkConfigs.SUDPH != nil {
		ar, err := arclient.NewHTTP(conf.NetworkConfigs.SUDPH.AddressResolver, conf.PubKey, conf.SecKey)
		if err != nil {
			return nil, err
		}

		conf := directtransport.ClientConfig{
			Type:            directtransport.SudphType,
			PK:              conf.PubKey,
			SK:              conf.SecKey,
			AddressResolver: ar,
		}

		clients.Direct[directtransport.SudphType] = directtransport.NewClient(conf)
	}

	return NewRaw(conf, clients), nil
}

// NewRaw creates a network from a config and a dmsg client.
func NewRaw(conf Config, clients NetworkClients) *Network {
	networks := make([]string, 0)

	if clients.DmsgC != nil {
		networks = append(networks, dmsg.Type)
	}

	for k, v := range clients.Direct {
		if v != nil {
			networks = append(networks, k)
		}
	}

	return &Network{
		conf:     conf,
		networks: networks,
		clients:  clients,
	}
}

// Init initiates server connections.
func (n *Network) Init() error {
	if n.clients.DmsgC != nil {
		time.Sleep(200 * time.Millisecond)
		go n.clients.DmsgC.Serve()
		time.Sleep(200 * time.Millisecond)
	}

	if n.conf.NetworkConfigs.STCP != nil {
		if client, ok := n.clients.Direct[directtransport.StcpType]; ok && client != nil && n.conf.NetworkConfigs.STCP.LocalAddr != "" {
			if err := client.Serve(); err != nil {
				return fmt.Errorf("failed to initiate 'stcp': %w", err)
			}
		} else {
			log.Infof("No config found for stcp")
		}
	}

	if n.conf.NetworkConfigs.STCPR != nil {
		if client, ok := n.clients.Direct[directtransport.StcprType]; ok && client != nil && n.conf.NetworkConfigs.STCPR.LocalAddr != "" {
			if err := client.Serve(); err != nil {
				return fmt.Errorf("failed to initiate 'stcpr': %w", err)
			}
		} else {
			log.Infof("No config found for stcpr")
		}
	}

	if n.conf.NetworkConfigs.STCPH != nil {
		if client, ok := n.clients.Direct[directtransport.StcphType]; ok && client != nil {
			if err := client.Serve(); err != nil {
				return fmt.Errorf("failed to initiate 'stcph': %w", err)
			}
		} else {
			log.Infof("No config found for stcph")
		}
	}

	if n.conf.NetworkConfigs.SUDP != nil {
		if client, ok := n.clients.Direct[directtransport.SudpType]; ok && client != nil && n.conf.NetworkConfigs.SUDP.LocalAddr != "" {
			if err := client.Serve(); err != nil {
				return fmt.Errorf("failed to initiate 'sudp': %w", err)
			}
		} else {
			log.Infof("No config found for sudp")
		}
	}

	if n.conf.NetworkConfigs.SUDPR != nil {
		if client, ok := n.clients.Direct[directtransport.SudprType]; ok && client != nil && n.conf.NetworkConfigs.SUDPR.LocalAddr != "" {
			if err := client.Serve(); err != nil {
				return fmt.Errorf("failed to initiate 'sudpr': %w", err)
			}
		} else {
			log.Infof("No config found for sudpr")
		}
	}

	if n.conf.NetworkConfigs.SUDPH != nil {
		if client, ok := n.clients.Direct[directtransport.SudphType]; ok && client != nil {
			if err := client.Serve(); err != nil {
				return fmt.Errorf("failed to initiate 'sudph': %w", err)
			}
		} else {
			log.Infof("No config found for sudph")
		}
	}

	return nil
}

// Close closes underlying connections.
func (n *Network) Close() error {
	wg := new(sync.WaitGroup)
	wg.Add(len(n.networks))

	var dmsgErr error
	if n.clients.DmsgC != nil {
		go func() {
			dmsgErr = n.clients.DmsgC.Close()
			wg.Done()
		}()
	}

	directErrors := make(map[string]error)

	for k, v := range n.clients.Direct {
		if v != nil {
			go func() {
				directErrors[k] = v.Close()
				wg.Done()
			}()
		}
	}

	wg.Wait()

	if dmsgErr != nil {
		return dmsgErr
	}

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
func (n *Network) TransportNetworks() []string { return n.networks }

// Dmsg returns underlying dmsg client.
func (n *Network) Dmsg() *dmsg.Client { return n.clients.DmsgC }

// STcp returns the underlying stcp.Client.
func (n *Network) STcp() directtransport.Client {
	return n.clients.Direct[directtransport.StcpType]
}

// STcpr returns the underlying stcpr.Client.
func (n *Network) STcpr() directtransport.Client {
	return n.clients.Direct[directtransport.StcprType]
}

// STcpH returns the underlying stcph.Client.
func (n *Network) STcpH() directtransport.Client {
	return n.clients.Direct[directtransport.StcphType]
}

// SUdp returns the underlying sudp.Client.
func (n *Network) SUdp() directtransport.Client {
	return n.clients.Direct[directtransport.SudpType]
}

// SUdpr returns the underlying sudpr.Client.
func (n *Network) SUdpr() directtransport.Client {
	return n.clients.Direct[directtransport.SudprType]
}

// SUdpH returns the underlying sudph.Client.
func (n *Network) SUdpH() directtransport.Client {
	return n.clients.Direct[directtransport.SudphType]
}

// Dial dials a visor by its public key and returns a connection.
func (n *Network) Dial(ctx context.Context, network string, pk cipher.PubKey, port uint16) (*Conn, error) {
	switch network {
	case dmsg.Type:
		addr := dmsg.Addr{
			PK:   pk,
			Port: port,
		}

		conn, err := n.clients.DmsgC.Dial(ctx, addr)
		if err != nil {
			return nil, fmt.Errorf("dmsg client: %w", err)
		}

		return makeConn(conn, network), nil
	default:
		client, ok := n.clients.Direct[network]
		if !ok {
			return nil, ErrUnknownNetwork
		}

		conn, err := client.Dial(ctx, pk, port)
		if err != nil {
			return nil, fmt.Errorf("sudph client: %w", err)
		}

		log.Infof("Dialed %v, conn local address %q, remote address %q", network, conn.LocalAddr(), conn.RemoteAddr())
		return makeConn(conn, network), nil
	}
}

// Listen listens on the specified port.
func (n *Network) Listen(network string, port uint16) (*Listener, error) {
	switch network {
	case dmsg.Type:
		lis, err := n.clients.DmsgC.Listen(port)
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
			return nil, fmt.Errorf("sudph client: %w", err)
		}

		return makeListener(lis, network), nil
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
