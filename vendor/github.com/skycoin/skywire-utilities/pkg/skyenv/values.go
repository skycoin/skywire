// Package skyenv pkg/skyenv/values.go
package skyenv

// Constants for new default services.
const (
	ServiceConfAddr     = "http://conf.skywire.skycoin.com"
	TpDiscAddr          = "http://tpd.skywire.skycoin.com"
	DmsgDiscAddr        = "http://dmsgd.skywire.skycoin.com"
	ServiceDiscAddr     = "http://sd.skycoin.com"
	RouteFinderAddr     = "http://rf.skywire.skycoin.com"
	UptimeTrackerAddr   = "http://ut.skywire.skycoin.com"
	AddressResolverAddr = "http://ar.skywire.skycoin.com"
	SetupPK             = "0324579f003e6b4048bae2def4365e634d8e0e3054a20fc7af49daf2a179658557"
	NetworkMonitorPK    = "0380ea88f0ad0aa4d93c330ba5f97aabca1d892190b94db69eee140b549d2817dd,0283bddb4357e2c4de0d470032cd809966aec65ce57e1188143ab32c7b589b38b6,02f4e33b75307267229b0c3d679d08dd23374333f558288cfcb114311a52199358,02090f03cb26c71779b8327067e2e37314d2db3e31dfe4f8f3cdd8e088a98eb7ec"
)

// Constants for testing deployment.
const (
	TestServiceConfAddr     = "http://conf.skywire.dev"
	TestTpDiscAddr          = "http://tpd.skywire.dev"
	TestDmsgDiscAddr        = "http://dmsgd.skywire.dev"
	TestServiceDiscAddr     = "http://sd.skywire.dev"
	TestRouteFinderAddr     = "http://rf.skywire.dev"
	TestUptimeTrackerAddr   = "http://ut.skywire.dev"
	TestAddressResolverAddr = "http://ar.skywire.dev"
	TestSetupPK             = "026c2a3e92d6253c5abd71a42628db6fca9dd9aa037ab6f4e3a31108558dfd87cf"
	TestNetworkMonitorPK    = "0218905f5d9079bab0b62985a05bd162623b193e948e17e7b719133f2c60b92093,0214456f6727b0dffacc3e4a9b331ff9bf7b7d97a9810c213772199f0f7ee59247,0394b6e4bdb50977658013089523cc77a9c3af8d1a1581855b496b9ae3126deea0,027f978ca206f00e052561b82a62e6405763f833779b1693fee9cc3c87caad26be"
)

// GetStunServers gives back default Stun Servers
func GetStunServers() []string {
	return []string{
		"139.162.12.30:3478",
		"170.187.228.181:3478",
		"172.104.161.184:3478",
		"170.187.231.137:3478",
		"143.42.74.91:3478",
		"170.187.225.78:3478",
		"143.42.78.123:3478",
		"139.162.12.244:3478",
	}
}

// DNSServer is value for DNS Server Address
const DNSServer = "1.1.1.1"
