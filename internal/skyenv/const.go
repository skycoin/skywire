package skyenv

// Constants for default services.
const (
	DefaultTpDiscAddr      = "http://transport.discovery.skywire.skycoin.com"
	DefaultDmsgDiscAddr    = "http://dmsg.discovery.skywire.skycoin.com"
	DefaultRouteFinderAddr = "http://routefinder.skywire.skycoin.com"
	DefaultSetupPK         = "0324579f003e6b4048bae2def4365e634d8e0e3054a20fc7af49daf2a179658557"
)

// Constants for testing deployment.
const (
	TestTpDiscAddr      = "http://transport.discovery.skywire.cc"
	TestDmsgDiscAddr    = "http://dmsg.discovery.skywire.cc"
	TestRouteFinderAddr = "http://routefinder.skywire.cc"
)

// Dmsg port constants.
const (
	DmsgSetupPort      = uint16(36)  // Listening port of a setup node.
	DmsgAwaitSetupPort = uint16(136) // Listening port of a visor for setup operations.
	DmsgTransportPort  = uint16(45)  // Listening port of a visor for incoming transports.
	DmsgHypervisorPort = uint16(46)  // Listening port of a visor for incoming hypervisor connections.
)

// Default dmsgpty constants.
const (
	DmsgPtyPort = uint16(22)

	DefaultDmsgPtyCLINet  = "unix"
	DefaultDmsgPtyCLIAddr = "/tmp/dmsgpty.sock"
)

// Default skywire app constants.
const (
	SkychatName = "skychat"
	SkychatPort = uint16(1)
	SkychatAddr = ":8000"

	SkysocksName = "skysocks"
	SkysocksPort = uint16(3)

	SkysocksClientName = "skysocks-client"
	SkysocksClientPort = uint16(13)
	SkysocksClientAddr = ":1080"
)
