package snet

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/SkycoinProject/skywire-mainnet/pkg/snet/stcp"

	"github.com/SkycoinProject/skycoin/src/util/logging"

	"github.com/SkycoinProject/dmsg"
	"github.com/SkycoinProject/dmsg/cipher"
	"github.com/SkycoinProject/dmsg/disc"
)

// Default ports.
// TODO(evanlinjin): Define these properly. These are currently random.
const (
	SetupPort      = uint16(36)  // Listening port of a setup node.
	AwaitSetupPort = uint16(136) // Listening port of a visor for setup operations.
	TransportPort  = uint16(45)  // Listening port of a visor for incoming transports.
)

// Network types.
const (
	DmsgType = dmsg.Type
	STCPType = stcp.Type
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
	return DmsgType
}

// STCPConfig defines config for STCP network.
type STCPConfig struct {
	PKTable   map[cipher.PubKey]string `json:"pk_table"`
	LocalAddr string                   `json:"local_address"`
}

// Type returns STCPType.
func (c *STCPConfig) Type() string {
	return STCPType
}

// Config represents a network configuration.
type Config struct {
	PubKey cipher.PubKey
	SecKey cipher.SecKey
	Dmsg   *DmsgConfig // The dmsg service will not be started if nil.
	STCP   *STCPConfig // The stcp service will not be started if nil.
}

// Network represents a network between nodes in Skywire.
type Network struct {
	conf     Config
	networks []string // networks to be used with transports
	dmsgC    *dmsg.Client
	stcpC    *stcp.Client
}

// New creates a network from a config.
func New(conf Config) *Network {
	var dmsgC *dmsg.Client
	var stcpC *stcp.Client

	if conf.Dmsg != nil {
		c := &dmsg.Config{
			MinSessions: conf.Dmsg.SessionsCount,
		}

		dmsgC = dmsg.NewClient(conf.PubKey, conf.SecKey, disc.NewHTTP(conf.Dmsg.Discovery), c)
		dmsgC.SetLogger(logging.MustGetLogger("snet.dmsgC"))
	}

	if conf.STCP != nil {
		stcpC = stcp.NewClient(conf.PubKey, conf.SecKey, stcp.NewTable(conf.STCP.PKTable))
		stcpC.SetLogger(logging.MustGetLogger("snet.stcpC"))
	}

	return NewRaw(conf, dmsgC, stcpC)
}

// NewRaw creates a network from a config and a dmsg client.
func NewRaw(conf Config, dmsgC *dmsg.Client, stcpC *stcp.Client) *Network {
	networks := make([]string, 0)

	if dmsgC != nil {
		networks = append(networks, DmsgType)
	}

	if stcpC != nil {
		networks = append(networks, STCPType)
	}

	return &Network{
		conf:     conf,
		networks: networks,
		dmsgC:    dmsgC,
		stcpC:    stcpC,
	}
}

// Init initiates server connections.
func (n *Network) Init() error {
	if n.dmsgC != nil {
		time.Sleep(200 * time.Millisecond)
		go n.dmsgC.Serve()
		time.Sleep(200 * time.Millisecond)
	}

	if n.conf.STCP != nil {
		if n.stcpC != nil && n.conf.STCP.LocalAddr != "" {
			if err := n.stcpC.Serve(n.conf.STCP.LocalAddr); err != nil {
				return fmt.Errorf("failed to initiate 'stcp': %v", err)
			}
		} else {
			fmt.Println("No config found for stcp")
		}
	}

	return nil
}

// Close closes underlying connections.
func (n *Network) Close() error {
	wg := new(sync.WaitGroup)
	wg.Add(2)

	var dmsgErr error
	go func() {
		dmsgErr = n.dmsgC.Close()
		wg.Done()
	}()

	var stcpErr error
	go func() {
		stcpErr = n.stcpC.Close()
		wg.Done()
	}()

	wg.Wait()

	if dmsgErr != nil {
		return dmsgErr
	}
	if stcpErr != nil {
		return stcpErr
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
func (n *Network) Dmsg() *dmsg.Client { return n.dmsgC }

// STcp returns the underlying stcp.Client.
func (n *Network) STcp() *stcp.Client { return n.stcpC }

// Dialer is an entity that can be dialed and asked for its type.
type Dialer interface {
	Dial(ctx context.Context, remote cipher.PubKey, port uint16) (net.Conn, error)
	Type() string
}

// Dial dials a visor by its public key and returns a connection.
func (n *Network) Dial(ctx context.Context, network string, pk cipher.PubKey, port uint16) (*Conn, error) {
	switch network {
	case DmsgType:
		addr := dmsg.Addr{
			PK:   pk,
			Port: port,
		}

		conn, err := n.dmsgC.Dial(ctx, addr)
		if err != nil {
			return nil, err
		}

		return makeConn(conn, network), nil
	case STCPType:
		conn, err := n.stcpC.Dial(ctx, pk, port)
		if err != nil {
			return nil, err
		}

		return makeConn(conn, network), nil
	default:
		return nil, ErrUnknownNetwork
	}
}

// Listen listens on the specified port.
func (n *Network) Listen(network string, port uint16) (*Listener, error) {
	switch network {
	case DmsgType:
		lis, err := n.dmsgC.Listen(port)
		if err != nil {
			return nil, err
		}

		return makeListener(lis, network), nil
	case STCPType:
		lis, err := n.stcpC.Listen(port)
		if err != nil {
			return nil, err
		}

		return makeListener(lis, network), nil
	default:
		return nil, ErrUnknownNetwork
	}
}

// Listener represents a listener.
type Listener struct {
	net.Listener
	lPK     cipher.PubKey
	lPort   uint16
	network string
}

func makeListener(l net.Listener, network string) *Listener {
	lPK, lPort := disassembleAddr(l.Addr())
	return &Listener{Listener: l, lPK: lPK, lPort: lPort, network: network}
}

// LocalPK returns a local public key of listener.
func (l Listener) LocalPK() cipher.PubKey { return l.lPK }

// LocalPort returns a local port of listener.
func (l Listener) LocalPort() uint16 { return l.lPort }

// Network returns a network of listener.
func (l Listener) Network() string { return l.network }

// AcceptConn accepts a connection from listener.
func (l Listener) AcceptConn() (*Conn, error) {
	conn, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}

	return makeConn(conn, l.network), nil
}

// Conn represent a connection between nodes in Skywire.
type Conn struct {
	net.Conn
	lPK     cipher.PubKey
	rPK     cipher.PubKey
	lPort   uint16
	rPort   uint16
	network string
}

func makeConn(conn net.Conn, network string) *Conn {
	lPK, lPort := disassembleAddr(conn.LocalAddr())
	rPK, rPort := disassembleAddr(conn.RemoteAddr())
	return &Conn{Conn: conn, lPK: lPK, rPK: rPK, lPort: lPort, rPort: rPort, network: network}
}

// LocalPK returns local public key of connection.
func (c Conn) LocalPK() cipher.PubKey { return c.lPK }

// RemotePK returns remote public key of connection.
func (c Conn) RemotePK() cipher.PubKey { return c.rPK }

// LocalPort returns local port of connection.
func (c Conn) LocalPort() uint16 { return c.lPort }

// RemotePort returns remote port of connection.
func (c Conn) RemotePort() uint16 { return c.rPort }

// Network returns network of connection.
func (c Conn) Network() string { return c.network }

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
			panic(fmt.Errorf("network.disassembleAddr: %v", err))
		}
	}

	return
}
