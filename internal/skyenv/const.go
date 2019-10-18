package skyenv

// Constants for default services.
const (
	DefaultTpDiscAddr      = "http://transport.discovery.skywire.skycoin.com"
	DefaultDmsgDiscAddr    = "http://messaging.discovery.skywire.skycoin.com"
	DefaultRouteFinderAddr = "http://routefinder.skywire.skycoin.com"
	DefaultSetupPK         = "026c5a07de617c5c488195b76e8671bf9e7ee654d0633933e202af9e111ffa358d"
)

// Constants for testing deployment.
const (
	TestTpDiscAddr      = "http://transport.discovery.skywire.cc"
	TestDmsgDiscAddr    = "http://dmsg.discovery.skywire.cc"
	TestRouteFinderAddr = "http://routefinder.skywire.cc"
)

// Common app constants.
const (
	AppProtocolVersion = "0.0.1"
)

// Default skywire apps.
const (
	SkychatName    = "skychat"
	SkychatVersion = "1.0"
	SkychatPort    = uint16(1)
	SkychatAddr    = ":8000"

	SkysshName    = "SSH"
	SkysshVersion = "1.0"
	SkysshPort    = uint16(2)

	SkyproxyName    = "socksproxy"
	SkyproxyVersion = "1.0"
	SkyproxyPort    = uint16(3)

	SkysshClientName    = "SSH-client"
	SkysshClientVersion = "1.0"
	SkysshClientPort    = uint16(12)
	SkysshClientAddr    = ":2222"

	SkyproxyClientName    = "socksproxy-client"
	SkyproxyClientVersion = "1.0"
	SkyproxyClientPort    = uint16(13)
	SkyproxyClientAddr    = ":1080"
	// TODO(evanlinjin): skyproxy-client requires
)
