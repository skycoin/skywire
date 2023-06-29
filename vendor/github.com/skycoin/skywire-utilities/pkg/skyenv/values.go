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
	RouteSetupPKs       = "0324579f003e6b4048bae2def4365e634d8e0e3054a20fc7af49daf2a179658557,024fbd3997d4260f731b01abcfce60b8967a6d4c6a11d1008812810ea1437ce438,03b87c282f6e9f70d97aeea90b07cf09864a235ef718725632d067873431dd1015"
	TPSetupPKs          = "03530b786c670fc7f5ab9021478c7ec9cd06a03f3ea1416c50c4a8889ef5bba80e,03271c0de223b80400d9bd4b7722b536a245eb6c9c3176781ee41e7bac8f9bad21,03a792e6d960c88c6fb2184ee4f16714c58b55f0746840617a19f7dd6e021699d9,0313efedc579f57f05d4f5bc3fbf0261f31e51cdcfde7e568169acf92c78868926,025c7bbf23e3441a36d7e8a1e9d717921e2a49a2ce035680fec4808a048d244c8a,030eb6967f6e23e81db0d214f925fc5ce3371e1b059fb8379ae3eb1edfc95e0b46,02e582c0a5e5563aad47f561b272e4c3a9f7ac716258b58e58eb50afd83c286a7f,02ddc6c749d6ed067bb68df19c9bcb1a58b7587464043b1707398ffa26a9746b26,03aa0b1c4e23616872058c11c6efba777c130a85eaf909945d697399a1eb08426d,03adb2c924987d8deef04d02bd95236c5ae172fe5dfe7273e0461d96bf4bc220be"
	NetworkMonitorPKs   = "0380ea88f0ad0aa4d93c330ba5f97aabca1d892190b94db69eee140b549d2817dd,0283bddb4357e2c4de0d470032cd809966aec65ce57e1188143ab32c7b589b38b6,02f4e33b75307267229b0c3d679d08dd23374333f558288cfcb114311a52199358,02090f03cb26c71779b8327067e2e37314d2db3e31dfe4f8f3cdd8e088a98eb7ec,03ff8dc39ed8d84be17a15b6a243edbcef1a5fd425209243fd7a9a28f0d23ddbea"
	SurveyWhitelistPKs  = "0327e2cf1d2e516ecbfdbd616a87489cc92a73af97335d5c8c29eafb5d8882264a,03abbb3eff140cf3dce468b3fa5a28c80fa02c6703d7b952be6faaf2050990ebf4,02b5ee5333aa6b7f5fc623b7d5f35f505cb7f974e98a70751cf41962f84c8c4637,03714c8bdaee0fb48f47babbc47c33e1880752b6620317c9d56b30f3b0ff58a9c3,020d35bbaf0a5abc8ec0ba33cde219fde734c63e7202098e1f9a6cf9daaeee55a9,027f7dec979482f418f01dfabddbd750ad036c579a16422125dd9a313eaa59c8e1,031d4cf1b7ab4c789b56c769f2888e4a61c778dfa5fe7e5cd0217fc41660b2eb65"
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
	TestTPSetupPKs          = "02225202d260d654d8a8be2cfe614900ac2f32ff465f321ff4641d58ce57d2be03,0222c7511b4c5539e3dfc5cfb8c133e331163040588bd6379e8e3e95986df2489a,0271775b76495331a68bbba41787465a28771d223a08f6a4d9399be8157ae44b4a,03967dfb31075378095ef3d3b3f8efdb3c78834a77bf53ea161b8443a9da04027a,03c8443df4c67f384b5e908467a443c4118a0cdaf57ca937973a2990a66d596b51,023d516a42103546367c1b75103ed842477967a4d779a964704f1e48fa2650e6e8,0204ecab637d2b4947c8bca09618fc6fc62e197c79b4e59715f6eeff407dc4df9b,03f911748d25edc0ff15544bfaa126a7b608df3d74dbf5e08d52b476b01e807dfb,027442361dfb57035c0873d74d96a858ad5e10c9ac645131f5d2c7ca7b15e42bc3"
	TestRouteSetupPKs       = "026c2a3e92d6253c5abd71a42628db6fca9dd9aa037ab6f4e3a31108558dfd87cf"
	TestNetworkMonitorPKs   = "0218905f5d9079bab0b62985a05bd162623b193e948e17e7b719133f2c60b92093,0214456f6727b0dffacc3e4a9b331ff9bf7b7d97a9810c213772199f0f7ee59247,0394b6e4bdb50977658013089523cc77a9c3af8d1a1581855b496b9ae3126deea0,027f978ca206f00e052561b82a62e6405763f833779b1693fee9cc3c87caad26be,02ecc42cbfdb3ac28b59249605daf1b929b9c46335bd1f3a53abe58f9aebe11e4c"
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
