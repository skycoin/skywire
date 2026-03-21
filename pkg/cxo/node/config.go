package node

import (
	"flag"
	"time"

	"github.com/skycoin/skycoin/src/cipher"

	"github.com/skycoin/skywire/pkg/cxo/node/log"
	"github.com/skycoin/skywire/pkg/cxo/skyobject"
	"github.com/skycoin/skywire/pkg/cxo/skyobject/registry"
)

// default configurations
const (
	Prefix                string        = "[node] "
	MaxConnections        int           = 1000 * 1000
	MaxPendingConnections int           = 1000
	MaxFillingTime        time.Duration = 10 * time.Minute
	MaxHeads              int           = 10
	ListenTCP             string        = ":8870"
	ListenUDP             string        = "" // don't listen
	RPCAddress            string        = ":8871"
	ResponseTimeout       time.Duration = 59 * time.Second
	Pings                 time.Duration = 118 * time.Second
	Public                bool          = false
)

// OnRootReceivedFunc represents callback that
// called when new Root objects received. It's
// possible to reject a received Root returning
// error by this function.
type OnRootReceivedFunc func(c *Conn, r *registry.Root) (reject error)

// OnRootFilledFunc represents callback that
// called when new Root filled and can be used.
// The callback called once per Root.
type OnRootFilledFunc func(n *Node, r *registry.Root)

// OnFillingBreaksFunc represents callback that
// called when a new Root object can't be filled.
type OnFillingBreaksFunc func(n *Node, r *registry.Root, err error)

// OnConnectFunc represents callback that called
// when a connection created and established. It's
// possible to terminate connection returning error
type OnConnectFunc func(c *Conn) (terminate error)

// OnDisconnectFunc represents callback that called
// when a connection closed.
type OnDisconnectFunc func(c *Conn, reason error)

// OnSubscribeRemoteFunc represents callback that
// called when a remote peer subscribes to some
// feed of the Node.
type OnSubscribeRemoteFunc func(c *Conn, feed cipher.PubKey) (reject error)

// OnUnsubscribeRemoteFunc represent callback that
// called when a remote peer unsubscribes from
// some feed.
type OnUnsubscribeRemoteFunc func(c *Conn, feed cipher.PubKey)

// OnPeerAddedFunc
type OnPeerAddedFunc func(f cipher.PubKey, p Peer)

// OnPeerUpdatedFunc
type OnPeerUpdatedFunc func(f cipher.PubKey, p Peer)

// OnPeerRemovedFunc
type OnPeerRemovedFunc func(f cipher.PubKey, p Peer)

// NetConfig represents configurations of
// a TCP or UDP network
type NetConfig struct {
	// Listen is listening address. Blank string
	// disables listening.
	Listen string

	// ResponseTimeout is timeout for requests.
	ResponseTimeout time.Duration

	// Pings is interval for pinging peers.
	Pings time.Duration
}

// SwarmConfig defines on how node finds peers, belonging
// to the same swarm, and how it interacts with them later.
type SwarmConfig struct {
	MaxPeers        uint64
	RequestPeerRate time.Duration

	PeerExpirePeriod  time.Duration
	ClearOldPeersRate time.Duration

	MaxConns         uint64
	OutgoingConnRate time.Duration

	PeersPerResponse uint64
}

func DefaultSwarmConfig() SwarmConfig {
	cfg := SwarmConfig{
		MaxPeers:        1000,
		RequestPeerRate: time.Minute,

		PeerExpirePeriod:  time.Hour * 24 * 7,
		ClearOldPeersRate: time.Minute * 10,

		MaxConns:         1000,
		OutgoingConnRate: time.Second * 5,

		PeersPerResponse: 30,
	}

	return cfg
}

// A Config represents configurations
// of the Node.
type Config struct {
	// SecKey allows to provide secret key.
	SecKey cipher.SecKey

	// Logger contains configurations of logger
	Logger log.Config

	// Config is configurations of skyobject.Container.
	*skyobject.Config

	// MaxConnections is limit of connections.
	MaxConnections int

	// MaxPendingConnections is limit of pending connections.
	MaxPendingConnections int

	// MaxHeads is limit of heads per feed.
	MaxHeads int

	// MaxFillingTime is time limit for filling of a Root object.
	MaxFillingTime time.Duration

	// RPC is RPC listening address. Empty string disables RPC.
	RPC string

	// TCP configurations
	TCP NetConfig

	// UDP configurations
	UDP NetConfig

	// Connection callbacks

	OnConnect    OnConnectFunc
	OnDisconnect OnDisconnectFunc

	// Public makes the Node share its list of feeds.
	Public bool

	// Subscription related callbacks

	OnSubscribeRemote   OnSubscribeRemoteFunc
	OnUnsubscribeRemote OnUnsubscribeRemoteFunc

	// Root related callbacks

	OnRootReceived  OnRootReceivedFunc
	OnRootFilled    OnRootFilledFunc
	OnFillingBreaks OnFillingBreaksFunc

	// Peer callbacks
	OnPeerAdded   OnPeerAddedFunc
	OnPeerUpdated OnPeerUpdatedFunc
	OnPeerRemoved OnPeerRemovedFunc
}

// NewConfig returns new Config with default values
func NewConfig() (c *Config) {

	c = new(Config)

	// logger
	c.Logger.Prefix = Prefix

	// container
	c.Config = skyobject.NewConfig()

	// node
	c.MaxConnections = MaxConnections
	c.MaxPendingConnections = MaxPendingConnections
	c.MaxFillingTime = MaxFillingTime
	c.MaxHeads = MaxHeads

	c.TCP.Listen = ListenTCP
	c.TCP.Pings = Pings
	c.TCP.ResponseTimeout = ResponseTimeout

	c.UDP.Listen = ListenUDP
	c.UDP.ResponseTimeout = ResponseTimeout

	c.RPC = RPCAddress
	c.Public = Public

	return

}

// FromFlags used to get values for the
// Config from command line flags.
func (c *Config) FromFlags() {

	// logger configs
	c.Logger.FromFlags()

	// container
	if c.Config != nil {
		c.Config.FromFlags()
	}

	// node

	flag.IntVar(&c.MaxConnections,
		"max-connections",
		c.MaxConnections,
		"max connections, incoming and outgoing, tcp and udp")

	flag.DurationVar(&c.MaxFillingTime,
		"max-filling-time",
		c.MaxFillingTime,
		"max time to fill a Root")

	flag.IntVar(&c.MaxHeads,
		"max-heads",
		c.MaxHeads,
		"max heads of a feed allowed")

	flag.StringVar(&c.RPC,
		"rpc",
		c.RPC,
		"RPC listening address")

	// TCP

	flag.StringVar(&c.TCP.Listen,
		"tcp",
		c.TCP.Listen,
		"tcp listening address")

	flag.DurationVar(&c.TCP.ResponseTimeout,
		"tcp-response-timeout",
		c.TCP.ResponseTimeout,
		"response timeout of TCP connections")

	flag.DurationVar(&c.TCP.Pings,
		"tcp-pings",
		c.TCP.Pings,
		"pings interval of TCP connections")

	// UDP

	flag.StringVar(&c.UDP.Listen,
		"udp",
		c.UDP.Listen,
		"udp listening address")

	flag.DurationVar(&c.UDP.ResponseTimeout,
		"udp-response-timeout",
		c.UDP.ResponseTimeout,
		"response timeout of UDP connections")

	flag.DurationVar(&c.UDP.Pings,
		"udp-pings",
		c.UDP.Pings,
		"pings interval of UDP connections")

	// public

	flag.BoolVar(&c.Public,
		"public",
		c.Public,
		"public server")

}

// Validate configurations.
func (c *Config) Validate() (err error) {

	// container
	if c.Config != nil {
		if err = c.Config.Validate(); err != nil {
			return
		}
	}

	return

}
