// Package visorapi pkg/visor/visorapi/api.go c3-vis-core
package visorapi

import (
	"encoding/json"
	"fmt"
	"net"
	"time"

	"github.com/google/uuid"
	"github.com/skycoin/skywire/pkg/app/appcommon"
	"github.com/skycoin/skywire/pkg/app/appnet"
	"github.com/skycoin/skywire/pkg/app/appserver"
	"github.com/skycoin/skywire/pkg/buildinfo"
	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/netutil"
	"github.com/skycoin/skywire/pkg/pty"
	"github.com/skycoin/skywire/pkg/router"
	"github.com/skycoin/skywire/pkg/router/setupmetrics"
	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/servicedisc"
	"github.com/skycoin/skywire/pkg/serviceuptime"
	"github.com/skycoin/skywire/pkg/transport"
	"github.com/skycoin/skywire/pkg/visor/dmsgtracker"
	"github.com/skycoin/skywire/pkg/visor/logserver"
	"github.com/skycoin/skywire/pkg/visor/netview"
)

// API represents visor API.
type API interface {
	//visor
	Overview() (*Overview, error)
	Summary() (*Summary, error)
	StateSnapshot() (*StateSnapshot, error)
	StateSnapshotProjected(fields []string) (*StateSnapshot, error)
	Health() (*HealthInfo, error)
	IsStartupComplete() bool
	EnableHypervisor() error
	DisableHypervisor() error
	EnableHypervisorPersist(persist bool) error
	DisableHypervisorPersist(persist bool) error
	IsHypervisorEnabled() bool
	EnableHypervisorUIPersist(persist bool) error
	DisableHypervisorUIPersist(persist bool) error
	IsHypervisorUIServing() bool
	SetHypervisorAuthPersist(enable, persist bool) error
	IsHypervisorAuthEnabled() bool
	DmsgPortHits() []dmsg.PortHit
	Uptime() (float64, error)
	UptimeHistory(args UptimeHistoryArgs) (*UptimeHistoryResponse, error)
	RuntimeStats() (*RuntimeStatsInfo, error)
	GoroutineDump() (string, error)
	Reload() error
	Suspend() error
	Resume() error
	IsSuspended() (bool, error)
	Shutdown() error
	RuntimeLogs() (string, error)
	RuntimeLogsSince(since int64) (RuntimeLogsDelta, error)
	HostStats() (*HostStatsInfo, error)
	NetworkView() (*NetworkViewResponse, error)
	SkychatPasswordIsSet() (bool, error)
	SetSkychatPassword(oldPassword, newPassword string) error
	ClearSkychatPassword(oldPassword string) error
	SkychatLocalAddr() (string, error)
	RemoteVisors() ([]string, error)
	DmsgPtyExec(args DmsgPtyExecArgs) (*pty.CommandExecResult, error)
	IsDMSGClientReady() (bool, error)
	DMSGServers() ([]DMSGServerInfo, error)
	Ports() (map[string]PortDetail, error)

	//reward setting
	SetRewardAddress(string) (string, error)
	GetRewardAddress() (string, error)
	DeleteRewardAddress() error

	// LAN DMSG server
	SetLANDmsgServer(LANDmsgServerInfo) error

	//app controls
	App(appName string) (*appserver.AppState, error)
	Apps() ([]*appserver.AppState, error)
	StartApp(appName string) error
	StartAppWithMode(appName, launcherMode string) error
	// SetAppRoutingPolicy installs (or clears, when path is "" /
	// "none") a per-app routing policy. Backend is dispatched by
	// file extension: "@/path.star" uses Starlark, "@/path.wasm"
	// uses WASM. The swap is live — the running app picks up the
	// new policy on its next dial without a restart.
	SetAppRoutingPolicy(appName, path string) error
	AddApp(appName, binaryName string) error
	DeleteApp(appName string) error
	RegisterApp(procConf appcommon.ProcConfig) (appcommon.ProcKey, error)
	DeregisterApp(procKey appcommon.ProcKey) error
	StopApp(appName string) error
	KillApp(appName string) error
	SetAppDetailedStatus(appName, state string) error
	SetAppError(appName, stateErr string) error
	RestartApp(appName string) error
	SetAutoStart(appName string, autostart bool) error
	SetAppWhitelist(appName, whitelist string) error
	// AddPtyWhitelist merges PKs into the visor's shared peer
	// whitelist (a connected hypervisor pushing its own hypervisors
	// for transitive pty + RPC trust).
	AddPtyWhitelist(pks []cipher.PubKey) error
	SetAppPK(appName string, pk cipher.PubKey) error
	SetAppSecure(appName string, isSecure bool) error
	SetAppAddress(appName string, address string) error
	SetAppKillswitch(appName string, killswitch bool) error
	SetAppNetworkInterface(appName string, netifc string) error
	SetAppDNS(appName string, dnsaddr string) error
	DoCustomSetting(appName string, customSetting map[string]any) error
	SetAppArgs(appName string, args []string) error
	// GetAppSettings / SetAppSettings carry the live tuning knobs held for an
	// app (pulled by the app, persisted by the visor; see api_app_settings.go).
	GetAppSettings(appName string) (AppSettings, error)
	SetAppSettings(appName string, vals map[string]int64, text map[string]string) (AppSettings, error)
	// CutAppTunnel closes exactly one of an app's tunnels — the route group
	// whose port is rgPort — and lets the app's own pool replace it.
	CutAppTunnel(appName string, rgPort uint16) (uint64, error)
	SetAppEnv(appName, key, value string) error
	SetAppEnvBatch(appName string, env map[string]string) error
	SetAppEnvFull(appName string, env []string) error
	SetAppLauncherMode(appName, mode string) error
	AppHelp(appName string) (string, error)
	LogsSince(timestamp time.Time, appName string) ([]string, error)
	RecentAppLog(appName, level string) ([]string, error)
	GetAppStats(appName string) (appserver.AppStats, error)
	GetAppError(appName string) (string, error)
	GetAppConnectionsSummary(appName string) ([]appserver.ConnectionSummary, error)

	//vpn controls
	StartVPNClient(pk cipher.PubKey) error
	StartVPNClientWithMode(pk cipher.PubKey, launcherMode string) error
	StopVPNClient(appName string) error
	VPNServers(version, country string) ([]servicedisc.Service, error)

	//skysocks-client controls
	StartSkysocksClient(pk string) error
	StopSkysocksClients() error
	ProxyServers(version, country string) ([]servicedisc.Service, error)
	TestProxy(config ProxyTestConfig) ([]ProxyTestResult, error)

	//transport settings
	SetExistingTPOnly(enabled bool) error
	SetForceLocalRoutes(enabled bool) error
	GetRouterSettings() (RouterSettings, error)
	SetRouterSettings(s RouterSettings) error
	SetMuxMode(mode string) error
	SetMuxCap(n int) error
	SetMuxWidth(n int) error
	SetMuxStandby(n int) error

	//transports
	TransportTypes() ([]string, error)
	Transports(types []string, pks []cipher.PubKey, logs bool) ([]*TransportSummary, error)
	Transport(tid uuid.UUID) (*TransportSummary, error)
	AddTransport(remote cipher.PubKey, tpType string, timeout time.Duration, label string, noRegister bool, skipLatencyProbe bool) (*TransportSummary, error)
	SetSTCPAddr(pk cipher.PubKey, addr string) error
	RemoveTransport(tid uuid.UUID) error
	RemoveAllTransports() error
	SetPublicAutoconnect(pAc bool) error
	SetIsPublic(isPublic bool) error
	GetIsPublic() bool
	GetRuntimeConfig() ([]byte, error)
	SetRuntimeConfig(rawJSON []byte) error
	SetConfigFields(fields map[string]json.RawMessage) ([]ConfigFieldChange, error)
	LocalTransportStats() (*LocalTransportStatsResponse, error)
	LocalUptimeStats(args LocalUptimeArgs) (*LocalUptimeResponse, error)
	FetchCXO(args FetchCXOArgs) (*FetchCXOResult, error)
	CXOStatus() ([]FeedStatus, error)
	CXORefreshFeed(args CXORefreshArgs) (*FeedStatus, error)
	GetConfigPath() (string, error)
	StartPublicAutoconnect() error
	StopPublicAutoconnect() error
	PublicAutoconnectStatus() (bool, error)
	GetPersistentTransports() ([]transport.PersistentTransports, error)
	SetPersistentTransports([]transport.PersistentTransports) error
	GetTransportLogs(days int) ([]TransportLogEntry, error)
	//transport discovery
	DiscoverTransportsByPK(pk cipher.PubKey) ([]*transport.Entry, error)
	DiscoverTransportByID(id uuid.UUID) (*transport.Entry, error)

	//routing
	RoutingRules() ([]routing.Rule, error)
	RoutingRule(key routing.RouteID) (routing.Rule, error)
	SaveRoutingRule(rule routing.Rule) error
	RemoveRoutingRule(key routing.RouteID) error
	RouteGroups() ([]RouteGroupInfo, error)
	RoutingStats() (routing.RoutingTableStats, error)
	// RoutingPolicies returns a snapshot of the currently-installed
	// routing-policy state: the visor-wide default plus any per-app
	// overrides. Empty struct when no policies are configured.
	// Hypervisor UI consumes this to display the policy panel in
	// the routing tab.
	RoutingPolicies() (*RoutingPoliciesSummary, error)
	// RouteGroupMuxInfo returns per-mux-leg byte/packet/latency
	// counters for every active route group tagged with the named
	// app (skysocks-client, vpn-client, etc.). Empty slice when
	// nothing is currently dialed via that app.
	RouteGroupMuxInfo(appName string) ([]MuxRouteGroupInfo, error)
	// AppDirectStreams reports the live DIRECT (0-hop) streams the named
	// app holds — the AppDirectMux shortcut's counterpart to
	// RouteGroupMuxInfo. A dial eligible for that shortcut builds no route
	// group, so an app using it has a well-defined path that the
	// route-group queries above cannot see. Empty appName reports all.
	AppDirectStreams(appName string) ([]transport.VStreamInfo, error)
	ActiveRoutes() ([]AppRouteStatus, error)
	AddMuxRoute(appName string, fwd, rev []routing.Hop, srcPort uint16) error
	GrowMuxRoute(appName string, target, minHops int, srcPort uint16) (int, error)
	// GrowMuxFromPool grows the app's tunnel by `legs` additional mux legs,
	// built on the plans of the app's STANDBY tunnels to the same exit
	// (already ranked, first-hop transport already up) and falling back to
	// GrowMuxRoute's route-finder path for whatever the pool cannot cover.
	GrowMuxFromPool(appName string, legs, minHops int, srcPort uint16) (int, error)
	RemoveMuxRoute(appName string, tpID uuid.UUID, srcPort uint16) error
	// SetMuxDirection pins (mode "default"/"flipped") or releases (mode
	// "auto") the unidirectional direction→leg-class mapping on ALL of the
	// app's active directional route groups. The pin is coordinated with the
	// peer over the wire; RouteGroupMuxInfo reports the live state.
	SetMuxDirection(appName, mode string) error
	// AddMuxRouteForward is AddMuxRoute with the reverse direction dropped:
	// the leg adds UPSTREAM send capacity only (`proxy mux add --forward-only`).
	AddMuxRouteForward(appName string, fwd, rev []routing.Hop, srcPort uint16) error
	// SetMuxWeights / ClearMuxWeights / MuxWeights are the operator's per-leg
	// send-weight override on one route group — the reachable form of the
	// router's WeightModeExplicit. Clearing returns the group to ECF.
	SetMuxWeights(appName string, weights map[string]float64, srcPort uint16) (router.MuxWeightsView, error)
	ClearMuxWeights(appName string, srcPort uint16) (router.MuxWeightsView, error)
	MuxWeights(appName string, srcPort uint16) (router.MuxWeightsView, error)
	// RouteGroupMuxNegotiated reports, per route group, the capabilities the
	// two ends negotiated and the send-window shape this end is applying.
	RouteGroupMuxNegotiated(appName string) ([]router.MuxNegotiated, error)
	// RehomeTunnelLeg moves a STANDBY tunnel's whole built chain into an ACTIVE
	// tunnel as one more mux leg, in place, with no setup-node dial
	// (`proxy mux adopt`). Both ports are the dst_port `proxy mux info` prints.
	RehomeTunnelLeg(appName string, targetPort, standbyPort uint16) error
	// GetRouterDialSettings / SetRouterDialSettings are the DIAL-TIME router
	// knobs: ranking priors, candidate counts, warm-plan cache shape, the
	// dead-route young-death window and the prefer-these-peers list.
	GetRouterDialSettings() (RouterDialSettings, error)
	SetRouterDialSettings(RouterDialSettings) error
	ServiceHealth() ([]ServiceHealthEntry, error)
	FetchServiceData(service, path string) ([]byte, error)
	SetMinHops(uint16) error
	GetMinHops() (uint16, error)
	SetCalculateRoutes(enabled bool) error
	GetCalculateRoutes() (bool, error)

	RegisterTCPPort(localPort int) error
	DeregisterTCPPort(localPort int) error
	ListTCPPorts() ([]int, error)
	RegisterForwardedPort(p ForwardedPort) error
	UpdateForwardedPort(p ForwardedPort) error
	ListForwardedPorts() ([]ForwardedPort, error)
	// DialUDPForward / StopUDPForward / ListUDPForwards drive client-side
	// faithful-UDP port forwarding (#2607): bridge a local UDP socket to
	// a remote forwarded_ports.udp service via DialPacket.
	DialUDPForward(remotePK cipher.PubKey, remotePort, localPort int) error
	StopUDPForward(localPort int) error
	ListUDPForwards() ([]int, error)
	ConnectRawTCP(network string, remotePK cipher.PubKey, remotePort, localPort int) (uuid.UUID, error)
	DisconnectRawTCP(id uuid.UUID) error
	ListRawTCP() (map[uuid.UUID]*appnet.RawTCPForwardConn, error)
	DialPing(config PingConfig) error
	Ping(config PingConfig) ([]time.Duration, error)
	PingOnce(config PingConfig) (time.Duration, error)
	StopPing(pk cipher.PubKey) error
	StopAllPings() (int, []string, error)
	DialDmsgPing(pk cipher.PubKey) error
	DialDmsgPingViaServer(pk cipher.PubKey, serverPK cipher.PubKey) error
	DialDmsgRPC(pk cipher.PubKey) (net.Conn, error)
	DmsgPing(conf PingConfig) ([]time.Duration, error)
	DmsgPingOnce(conf PingConfig) (time.Duration, error)
	StopDmsgPing(pk cipher.PubKey) error
	GetDmsgPingServerPK(pk cipher.PubKey) (cipher.PubKey, error)
	GetRemoteDmsgServers(pk cipher.PubKey) ([]cipher.PubKey, error)
	GetPreferredDmsgServer(remotePK cipher.PubKey) (cipher.PubKey, error)
	BandwidthTest(conf BandwidthTestConfig) (BandwidthResult, error)
	DmsgBandwidthTest(conf BandwidthTestConfig) (BandwidthResult, error)

	TestVisor(config PingConfig) ([]TestResult, error)

	ReinitiateModule(module string) error

	//service discovery management (network monitor functionality)
	DeregisterService(pks []cipher.PubKey, serviceType string) error

	//ui server controls
	StartUIServer(addr string) error
	StopUIServer() error
	UIServerStatus() (*UIServerStatus, error)

	//dmsg utilities
	DmsgProbe(pk cipher.PubKey, port uint16) (bool, error)
	DmsgProbeReason(pk cipher.PubKey, port uint16) (bool, string, error)
	DmsgProbeViaServer(pk cipher.PubKey, port uint16, serverPK cipher.PubKey) (bool, error)
	SkynetProbe(pk cipher.PubKey, port uint16) (bool, error)
	DmsgHTTP(req DmsgHTTPRequest) (*DmsgHTTPResponse, error)
	SkynetHTTP(req SkynetHTTPRequest) (*SkynetHTTPResponse, error)
	VisorSCP(req VisorSCPRequest) error
	VisorCat(req VisorCatRequest) (*VisorCatResponse, error)
	DmsgConnectAll() (*DmsgConnectAllResult, error)
	SetDmsgSessionsCount(count int) (*DmsgConnectAllResult, error)
	DmsgSessions() (*DmsgClientSessions, error)
	DmsgConverge(carriers []string) (*DmsgConvergeResult, error)

	// CXO user feeds — visor-published TreeStore feeds beyond the
	// always-on telemetry one. See pkg/visor/cxo_user_feeds.go.
	RegisterCXOFeed(name string, dmsgPort uint16, description string) error
	UnregisterCXOFeed(name string) error
	ListCXOFeeds() []logserver.CXOFeedEntry

	// Chat-pair feeds — per-partner CXO feeds with read-side
	// allowlists. See pkg/visor/pairing.go.
	PairAdd(peerPK cipher.PubKey) error
	PairList() ([]PairInfo, error)
	PairRemove(peerPK cipher.PubKey) error
	PairMarkActive(peerPK cipher.PubKey) error
	PairSend(peerPK cipher.PubKey, text string) (string, error)
	PairDelete(peerPK cipher.PubKey, id string) error
	PairPoll(since time.Time) ([]PairMessage, error)

	// Skychat profile — this visor's published display name and avatar,
	// and a peer's. See pkg/visor/profile.go.
	ProfileGet() (Profile, error)
	ProfileSet(args ProfileSetArgs) (Profile, error)
	ProfileFetch(pk cipher.PubKey) (Profile, error)

	// Chat-group feeds — D1 owner-centric CXO feeds with multi-PK
	// allowlists. See pkg/visor/group.go.
	GroupCreate(args GroupCreateArgs) (GroupInfo, string, error)
	GroupJoin(args GroupJoinArgs) (GroupInfo, error)
	GroupResolve(args GroupResolveArgs) (GroupResolveResult, error)
	GroupAskAgain(id string) (GroupInfo, error)
	GroupList() ([]GroupInfo, error)
	GroupGet(id string) (GroupInfo, error)
	GroupInvite(id string) (string, error)
	GroupAddMember(id string, pk cipher.PubKey) (GroupInfo, error)
	GroupJoinRequests(id string) ([]GroupJoinRequest, error)
	GroupApproveJoin(id string, pk cipher.PubKey) (GroupInfo, error)
	GroupDenyJoin(id string, pk cipher.PubKey) error
	GroupRemoveMember(id string, pk cipher.PubKey) (GroupInfo, error)
	GroupBanMember(id string, pk cipher.PubKey) (GroupInfo, error)
	GroupUnbanMember(id string, pk cipher.PubKey) (GroupInfo, error)
	GroupMuteMember(id string, pk cipher.PubKey) (GroupInfo, error)
	GroupUnmuteMember(id string, pk cipher.PubKey) (GroupInfo, error)
	GroupSetReadOnly(id string, readOnly bool) (GroupInfo, error)
	GroupSetListed(id string, listed bool) (GroupInfo, error)
	GroupSetMeta(args GroupSetMetaArgs) (GroupInfo, error)
	GroupRefreshMeta(id string) (GroupInfo, error)
	GroupCatalog(host cipher.PubKey) ([]GroupCatalogEntry, bool, error)
	GroupPromoteAdmin(id string, pk cipher.PubKey) (GroupInfo, error)
	GroupDemoteAdmin(id string, pk cipher.PubKey) (GroupInfo, error)
	GroupRotateKey(id string) (GroupInfo, error)
	GroupSetPeerBackfill(id string, enabled bool) (GroupInfo, error)
	GroupSetJoinPoW(id string, bits uint8) (GroupInfo, error)
	GroupSend(args GroupSendArgs) error
	GroupFileKey(args GroupFileKeyArgs) (GroupFileKeyResult, error)
	GroupUnsend(args GroupUnsendArgs) error
	GroupPoll(since time.Time) ([]GroupMessage, error)
	GroupDelete(id string) error
	GroupLeave(id string) error
	GroupHistory(groupID string, limit int) ([]GroupMessage, error)
	GroupHistoryPage(args GroupHistoryPageArgs) ([]GroupMessage, error)
	GroupHistoryGroups() ([]string, error)

	// Skychat 1:1 voice calls (pkg/skychat/call).
	VoiceCall(peer cipher.PubKey) (string, error)
	// VoiceDial places a call and returns its id without waiting for an
	// answer — what a UI needs, and what an HTTP handler can actually
	// deliver. See Visor.VoiceDial.
	VoiceDial(peer cipher.PubKey) (string, error)
	VoiceHangup(callID string) error
	VoiceActive() ([]string, error)
	VoiceAnswer(callID string) error
	VoiceDecline(callID string) error
	VoiceIncoming() ([]string, error)
	// VoiceDialing lists the calls this visor is PLACING and that have not
	// been answered yet. It is what lets a UI show "calling…" and, more to
	// the point, call it off: hanging up takes a call id, and until the
	// invite is answered this is the only place one exists.
	VoiceDialing() ([]VoiceDialingInfo, error)
	VoiceCallAudio(callID string) (sent, recv []int16, err error)
	VoiceMute(callID string, mic, speaker bool) error

	// Embedded Transport Setup Node (TPS) controls
	TPSStatus() (*TPSStatus, error)
	TPSAddTransport(targetPK, remotePK cipher.PubKey, tpType string) (*TPSTransportResponse, error)
	TPSRemoveTransport(targetPK cipher.PubKey, tpID uuid.UUID) error
	TPSGetTransports(targetPK cipher.PubKey) ([]TPSTransportResponse, error)

	// External TPS operations (dial external TPS over dmsg)
	GetTransportSetupNodes() ([]cipher.PubKey, error)
	GetTransportSetupNodesSorted() ([]cipher.PubKey, error)
	GetRouteSetupNodesSorted() ([]cipher.PubKey, error)
	GetTPSHealth() ([]NodeHealth, error)
	GetRSNHealth() ([]NodeHealth, error)

	// Embedded Route Setup Node (RSN) stats
	RouteSetupStats() (*setupmetrics.StatsSnapshot, error)

	// EmbeddedProxies reports the runtime state of the in-process
	// .dmsg / .skynet resolving proxies. Hypervisor UI consumes this
	// to render the "browser proxy" widget — listener addresses,
	// domain suffix, running state.
	EmbeddedProxies() (*EmbeddedProxiesStatus, error)

	// SetEmbeddedProxyEnabled flips a resolver on or off at runtime.
	// `kind` is "dmsg" or "skynet"; `enable` true starts the
	// resolver, false stops it. Idempotent. Only affects the live
	// runtime — the on-disk config is unchanged, so a visor restart
	// reverts to the config's Enable flag.
	SetEmbeddedProxyEnabled(kind string, enable bool) error
	SetEmbeddedProxyUpstream(kind, addr string) error
	SetEmbeddedProxyBind(kind, addr string) error
	ResetRouteSetupStats() error

	TPSExternalHealthCheck(tpsPK cipher.PubKey) error
	TPSExternalAddTransport(tpsPK, targetPK, remotePK cipher.PubKey, tpType string) (*TPSTransportResponse, error)
	TPSExternalGetTransports(tpsPK, targetPK cipher.PubKey) ([]TPSTransportResponse, error)

	// DMSG diagnostics
	DmsgPorterStats() (*DmsgPorterStatus, error)
	DmsgPorterReset() (*DmsgPorterStatus, error)
	DmsgPorterDiag() (*netutil.EphemeralDiagResult, error)
	DmsgReconnect() (int, error)
	DmsgSetMinSessions(n int) error
	AddHypervisor(pk cipher.PubKey) error
	// PendingHypervisors lists peers waiting to be approved as hypervisors
	// (a same-origin transport or a refused RPC), by fingerprint.
	PendingHypervisors() ([]PendingHypervisor, error)
	// ApproveHypervisor approves a pending key by public key or fingerprint
	// (or unique prefix); it is AddHypervisor with a lookup in front.
	ApproveHypervisor(sel string) (cipher.PubKey, error)
	// NewPairCode mints a one-time pairing code valid for ttl (0 = default).
	NewPairCode(ttl time.Duration) (PairCode, error)
	RemoveHypervisor(pk cipher.PubKey) error
	RemoveAllHypervisors() (int, error)
	SetHypervisorPassword(oldPassword, newPassword string) error
	SetHypervisorPasswordForce(newPassword string) error
	CheckAREntry(pk string) ([]string, error)
	ARSelfInfo() (*ARSelfRegistration, error)
	TransportRPCCall(remotePK cipher.PubKey, method string, args json.RawMessage) (json.RawMessage, error)
	HVListVisors() ([]HVVisorEntry, error)
	HVListDirectVisors() ([]HVVisorEntry, error)
	HVListVisorsTree() (*HVVisorTree, error)
	HVVisorSummary(pk cipher.PubKey) (*Summary, error)
	HVStartApp(pk cipher.PubKey, appName string) error
	HVStopApp(pk cipher.PubKey, appName string) error
	HVSetMinHops(pk cipher.PubKey, hops uint16) error
	HVSetRewardAddress(pk cipher.PubKey, addr string) (string, error)
	HVRemoveTransport(pk cipher.PubKey, tid uuid.UUID) error
	HVRemoveRoutingRule(pk cipher.PubKey, key routing.RouteID) error
	HVAddTransport(pk, remote cipher.PubKey, tpType, label string, timeout time.Duration) (*TransportSummary, error)
	HVSetPublicAutoconnect(pk cipher.PubKey, enable bool) error
	HVSetCalculateRoutes(pk cipher.PubKey, enable bool) error
	HVReload(pk cipher.PubKey) error
	HVShutdown(pk cipher.PubKey) error
	HVServiceHealth(pk cipher.PubKey) ([]ServiceHealthEntry, error)
	HVDmsgSessions(pk cipher.PubKey) (*DmsgClientSessions, error)
	HVDmsgConnectAll(pk cipher.PubKey) (*DmsgConnectAllResult, error)
	HVSetDmsgSessionsCount(pk cipher.PubKey, count int) (*DmsgConnectAllResult, error)
	HVLogsSince(pk cipher.PubKey, since time.Time, appName string) ([]string, error)
	HVSetAutoStart(pk cipher.PubKey, appName string, autostart bool) error
	HVEmbeddedProxies(pk cipher.PubKey) (*EmbeddedProxiesStatus, error)
	HVSetEmbeddedProxyEnabled(pk cipher.PubKey, kind string, enable bool) error
	HVSetEmbeddedProxyUpstream(pk cipher.PubKey, kind, addr string) error
	HVListTCPPorts(pk cipher.PubKey) ([]int, error)
	HVRegisterTCPPort(pk cipher.PubKey, port int) error
	HVDeregisterTCPPort(pk cipher.PubKey, port int) error
	HVListForwardedPorts(pk cipher.PubKey) ([]ForwardedPort, error)
	HVRegisterForwardedPort(pk cipher.PubKey, p ForwardedPort) error
	HVUpdateForwardedPort(pk cipher.PubKey, p ForwardedPort) error

	// Close closes the API connection (for RPC clients)
	Close() error
}

// UIServerStatus contains the status of the UI server.
type UIServerStatus struct {
	Running   bool   `json:"running"`
	LocalAddr string `json:"local_addr,omitempty"`
	DmsgPort  uint16 `json:"dmsg_port,omitempty"`
}

// EmbeddedProxyInfo describes the state of one in-process resolving
// proxy (dmsgweb or skynetweb). The hypervisor UI renders this so
// users can copy the SOCKS5 address into their browser without
// poking at the visor config directly.
type EmbeddedProxyInfo struct {
	// Enabled is the config flag value, reflecting the intended
	// state. Running is the observed state; mismatches happen
	// briefly during Start/Stop or when a dependency is still
	// bootstrapping.
	Enabled bool `json:"enabled"`
	// Running is true once the resolver goroutine has been spawned.
	Running bool `json:"running"`
	// DomainSuffix is the TLD matched by the resolver (e.g. ".dmsg").
	DomainSuffix string `json:"domain_suffix,omitempty"`
	// SocksAddr is the localhost SOCKS5 listener (e.g. "127.0.0.1:4445").
	// Empty when disabled or when Config.ProxyPort is 0.
	SocksAddr string `json:"socks_addr,omitempty"`
	// UpstreamSOCKS is the configured fallthrough, empty for direct.
	UpstreamSOCKS string `json:"upstream_socks,omitempty"`
	// Stats is the cumulative request counter snapshot. Zero-valued
	// when the resolver has never been constructed.
	Stats *EmbeddedProxyStats `json:"stats,omitempty"`
}

// EmbeddedProxyStats is a JSON-friendly stats snapshot common to
// dmsgweb and skynetweb. Mirrors pkg/dmsgweb.StatsSnapshot /
// pkg/skynetweb.StatsSnapshot shapes — duplicated here so the RPC
// surface stays decoupled from the internal stats types.
type EmbeddedProxyStats struct {
	StartedAt     time.Time  `json:"started_at,omitempty"`
	UptimeSec     int64      `json:"uptime_sec,omitempty"`
	TotalRequests uint64     `json:"total_requests"`
	Successful    uint64     `json:"successful"`
	Failed        uint64     `json:"failed"`
	Active        int64      `json:"active"`
	LastRequestAt *time.Time `json:"last_request_at,omitempty"`
	LastSuccessAt *time.Time `json:"last_success_at,omitempty"`
	LastFailureAt *time.Time `json:"last_failure_at,omitempty"`
	LastError     string     `json:"last_error,omitempty"`
}

// EmbeddedProxiesStatus bundles the state of every in-process
// resolving proxy. Separate fields (not a map) so the UI can treat
// each proxy's presence/absence as a hard-coded toggle.
type EmbeddedProxiesStatus struct {
	DmsgWeb       *EmbeddedProxyInfo   `json:"dmsg_web,omitempty"`
	SkynetWeb     *EmbeddedProxyInfo   `json:"skynet_web,omitempty"`
	SkymailBridge *EmbeddedSkymailInfo `json:"skymail_bridge,omitempty"`
}

// EmbeddedSkymailInfo is the runtime snapshot for the in-process
// SMTP bridge. Distinct from EmbeddedProxyInfo (which is SOCKS5-
// shaped) because the bridge has no upstream-SOCKS concept and its
// listener is an SMTP server, not a SOCKS5 proxy.
type EmbeddedSkymailInfo struct {
	Enabled bool   `json:"enabled"`
	Running bool   `json:"running"`
	Addr    string `json:"addr,omitempty"`
	Mode    string `json:"mode,omitempty"`
	Suffix  string `json:"suffix,omitempty"`
}

// TPSStatus contains the status of the embedded Transport Setup Node.
type TPSStatus struct {
	Enabled bool          `json:"enabled"`
	PubKey  cipher.PubKey `json:"pub_key,omitempty"`
}

// TPSTransportResponse contains information about a transport managed by TPS.
type TPSTransportResponse struct {
	ID     uuid.UUID     `json:"id"`
	Local  cipher.PubKey `json:"local"`
	Remote cipher.PubKey `json:"remote"`
	Type   string        `json:"type"`
}

// Overview provides a range of basic information about a Visor.
type Overview struct {
	PubKey          cipher.PubKey         `json:"local_pk"`
	BuildInfo       *buildinfo.Info       `json:"build_info"`
	AppProtoVersion string                `json:"app_protocol_version"`
	Apps            []*appserver.AppState `json:"apps"`
	Transports      []*TransportSummary   `json:"transports"`
	RoutesCount     int                   `json:"routes_count"`
	LocalIP         string                `json:"local_ip"`
	PublicIP        string                `json:"public_ip"`
	IsSymmetricNAT  bool                  `json:"is_symmetic_nat"`
	// NATType is the visor's STUN-classified NAT type (e.g. "Full cone NAT",
	// "Symmetric NAT"), determined once at startup. Surfaced so `cli visor ip`
	// reports the visor's already-known result instead of re-running STUN.
	NATType             string          `json:"nat_type,omitempty"`
	CountryCode         string          `json:"country_code,omitempty"`
	RegionName          string          `json:"region_name,omitempty"`
	CityName            string          `json:"city_name,omitempty"`
	Latitude            float64         `json:"latitude,omitempty"`
	Longitude           float64         `json:"longitude,omitempty"`
	Hypervisors         []cipher.PubKey `json:"hypervisors"`
	ConnectedHypervisor []cipher.PubKey `json:"connected_hypervisor"`
	// Hostname is the operating-system hostname the visor process
	// sees at the time of the Overview call. Best-effort:
	// os.Hostname() failures (sandbox / containerized environments
	// missing the syscall) yield an empty string rather than
	// erroring out the whole Overview. Surfaced to operators in the
	// hypervisor UI as the default label fallback when no explicit
	// label is set, and shown in `cli visor info`.
	Hostname string `json:"hostname,omitempty"`
}

// Summary provides detailed info including overview and health of the visor.
type Summary struct {
	Overview     *Overview         `json:"overview"`
	Health       *HealthInfo       `json:"health"`
	Uptime       float64           `json:"uptime"`
	Routes       []RoutingRuleResp `json:"routes"`
	RouteGroups  []RouteGroupInfo  `json:"route_groups,omitempty"`
	IsHypervisor bool              `json:"is_hypervisor,omitempty"`
	// HypervisorAddr is the host:port the hypervisor UI is bound to
	// when IsHypervisor=true. Empty when this visor isn't hosting a
	// hypervisor (or when v.conf.Hypervisor is nil). Sourced from
	// v.conf.Hypervisor.HTTPAddr.
	HypervisorAddr       string                         `json:"hypervisor_addr,omitempty"`
	DmsgStats            *dmsgtracker.DmsgClientSummary `json:"dmsg_stats"`
	ConnectedDmsgServers []string                       `json:"connected_dmsg_servers"` // Deprecated: use DMSGServers instead
	DMSGServers          []DMSGServerInfo               `json:"dmsg_servers"`           // Connected DMSG servers with latencies
	Online               bool                           `json:"online"`
	// OfflineSince is set on cached summaries served when the live
	// RPC fails. Lets the UI render a "offline since X" indicator
	// alongside the otherwise-stale fields. nil when Online=true.
	OfflineSince *time.Time `json:"offline_since,omitempty"`
	// LastSeenAt is the timestamp of the last successful live
	// summary fetch. Set on both fresh and cached responses so the
	// UI can show "as of HH:MM:SS" even on stale rows. nil only on
	// the never-seen path.
	LastSeenAt           *time.Time                       `json:"last_seen_at,omitempty"`
	MinHops              uint16                           `json:"min_hops"`
	PersistentTransports []transport.PersistentTransports `json:"persistent_transports"`
	RewardAddress        string                           `json:"reward_address"`
	// RewardEligible reflects the reward system's verdict on this visor's most
	// recent survey PUSH: non-nil false = REJECTED (e.g. version below the reward
	// floor) — the hypervisor UI shows a red mark, distinct from the hyphen for no
	// reward address. nil = no verdict yet (never pushed / no reward address).
	RewardEligible         *bool  `json:"reward_eligible,omitempty"`
	RewardIneligibleReason string `json:"reward_ineligible_reason,omitempty"`
	BuildTag               string `json:"build_tag"`
	ConfigVersion          string `json:"config_version"`
	PublicAutoconnect      bool   `json:"public_autoconnect"`
	IsPublic               bool   `json:"is_public"`
	// Load is a lightweight resource snapshot (load average, mem %, disk %)
	// for the `hv ls --load` view. omitempty so the field is absent on
	// summaries from older visors that don't populate it.
	Load *LoadStats `json:"load,omitempty"`
}

// HealthInfo carries information about visor's services health.
// ServicesHealth is the aggregate status — "connecting" if any subsystem is
// unhealthy, "healthy" only when all are healthy. The per-subsystem fields
// let the UI show which specific subsystem is the blocker rather than
// a generic label.
type HealthInfo struct {
	ServicesHealth         string `json:"services_health"`
	UptimeTrackerHealth    string `json:"uptime_tracker_health,omitempty"`
	AutoconnectHealth      string `json:"autoconnect_health,omitempty"`
	TransportabilityHealth string `json:"transportability_health,omitempty"`
}

// DmsgPtyExecArgs is the request shape for API.DmsgPtyExec.
// RemotePK identifies the target visor's dmsgpty host; RemotePort
// defaults to pty.DefaultPort (22) when zero. Req carries the
// command, arguments, optional environment overrides, optional stdin,
// and per-call timeout — see pty.CommandExecReq.
//
// Scheme overrides the dialer choice. The visor's dmsgpty Host is
// wired with a MultiDialer that tries skynet first (transport-aware,
// rides existing transports for low latency) and falls back to dmsg
// on miss. That ordering breaks down when skynet's dial blocks past
// the caller's ctx without honoring it — dmsg never gets tried and
// the whole exec hangs. Operators who know the peer has a working
// dmsg session can pass Scheme="dmsg" to skip skynet entirely. Empty
// keeps the default MultiDialer behavior.
type DmsgPtyExecArgs struct {
	RemotePK   cipher.PubKey
	RemotePort uint16
	Req        pty.CommandExecReq
	// Scheme: "" (default — MultiDialer chain), "dmsg" (force dmsg
	// only), or "skynet" (force skynet only). Unknown values
	// return a clear error rather than silently falling back.
	Scheme string
}

// UptimeHistoryArgs is the request shape for API.UptimeHistory. All
// fields are optional: empty Since reads everything in retention,
// zero Limit returns all rows, IncludeTimeline=false skips the
// timeline byte slice.
type UptimeHistoryArgs struct {
	Since           time.Time
	Limit           int
	IncludeTimeline bool
	// TimelineDate, when set with IncludeTimeline, returns the bitmap
	// for that UTC date instead of today.
	TimelineDate time.Time
}

// UptimeHistoryResponse carries the visor's own session history.
// Current is the in-flight session row; Sessions is everything the
// recorder has retained, oldest first. Timeline (when requested) is
// the 36-byte 288-slot bitmap for TimelineDate, suitable for
// rendering with serviceuptime.FormatBitmap.
type UptimeHistoryResponse struct {
	Current      serviceuptime.SessionRecord   `json:"current"`
	Sessions     []serviceuptime.SessionRecord `json:"sessions"`
	Timeline     []byte                        `json:"timeline,omitempty"`
	TimelineDate string                        `json:"timeline_date,omitempty"`
}

// RuntimeStatsInfo carries Go runtime statistics for the visor process.
type RuntimeStatsInfo struct {
	NumGoroutine int    `json:"num_goroutine"`
	NumCPU       int    `json:"num_cpu"`
	GOMAXPROCS   int    `json:"gomaxprocs"`
	GoVersion    string `json:"go_version"`
	// Memory stats in bytes
	MemAlloc      uint64 `json:"mem_alloc"`
	MemTotalAlloc uint64 `json:"mem_total_alloc"`
	MemSys        uint64 `json:"mem_sys"`
	MemHeapAlloc  uint64 `json:"mem_heap_alloc"`
	MemHeapSys    uint64 `json:"mem_heap_sys"`
	NumGC         uint32 `json:"num_gc"`
}

// RouteHopInfo contains detailed information about a single hop in a route.
type RouteHopInfo struct {
	TpID   string
	From   string
	To     string
	TpType string
}

// PingConfig use as configuration for ping command
type PingConfig struct {
	PK          cipher.PubKey
	Tries       int
	PcktSize    int
	PubVisCount int
	LocalRoute  bool           // Skip route finder and use local route calculation
	TransportID string         // Optional: use specific transport (skips route calculation)
	ForwardHops []RouteHopInfo // Optional: explicit forward route (skips route calculation)
	ReverseHops []RouteHopInfo // Optional: explicit reverse route (skips route calculation)
	// RouteIndex identifies which of multiple parallel routes to the
	// same peer this PingConfig refers to. Zero (the default) means
	// "primary / single route" and preserves all legacy single-route
	// behavior. Non-zero values are used by mux-aware callers
	// (`cli visor ping mux-bw`) to dial multiple routes to the same
	// target without their conns trampling each other in the visor's
	// per-route conn map. See PingRouteRef in pkg/visor/ping.go.
	RouteIndex int
	// MinHops, when > 0, forces the route-finder to skip paths with
	// fewer than this many hops on THIS dial — overrides the visor-
	// global routing.min_hops config for the duration of the call.
	// Plumbed straight into router.DialOptions.MinHops. Used by
	// `cli visor ping mux-bw --min-hops N` to verify the operator's
	// "mux via intermediates > direct" hypothesis: without it, the
	// router's direct-transport fast path would short-circuit every
	// dial to use the direct stcpr whenever one existed. 0 = inherit
	// visor-global setting.
	MinHops int
	// SetupTimeout, when > 0, overrides DialPing's hardcoded 30s
	// dial timeout. Multi-hop route setup through 4+ intermediates
	// can take 30-120s (route-finder + setup-node round trips +
	// saveRouteGroupRules retries), exceeding the legacy 30s
	// ceiling. mux-bw passes its cfg.SetupTimeout here. 0 falls
	// back to the existing 30s default.
	SetupTimeout time.Duration
	// Timeout bounds a single PingOnce round-trip (ack + echo reads).
	// 0 = 10s default. mux-bw's probe loop sets this to the remaining
	// pump/idle window so a stuck probe can't outlive the measurement
	// and hang the run.
	Timeout time.Duration
}

// TestResult type of test result
type TestResult struct {
	PK     string
	Max    string
	Min    string
	Mean   string
	Status string
}

// ProxyTestConfig configures proxy testing
type ProxyTestConfig struct {
	Servers []cipher.PubKey `json:"servers"`  // Proxy servers to test
	TestURL string          `json:"test_url"` // URL to fetch through proxy (default from deployment.Prod.GeoIP)
	Timeout time.Duration   `json:"timeout"`  // Timeout per test (default: 30s)
}

// ProxyTestResult contains the result of a single proxy test
type ProxyTestResult struct {
	PK       string `json:"pk"`       // Proxy server public key
	Status   string `json:"status"`   // "OK", "FAIL", "TIMEOUT"
	Duration int64  `json:"duration"` // Duration in milliseconds
	IP       string `json:"ip"`       // IP returned by test
	Location string `json:"location"` // Geo location (City, Country)
	Error    string `json:"error"`    // Error message if failed
}

// AppRouteStatus combines route status with the app that owns it.
type AppRouteStatus struct {
	AppName string             `json:"app_name"`
	Route   router.RouteStatus `json:"route"`
}

// ServiceHealthEntry represents the health status of a deployment service.
type ServiceHealthEntry struct {
	Name      string `json:"name"`
	URL       string `json:"url"`
	Status    string `json:"status"`
	Version   string `json:"version,omitempty"`
	Error     string `json:"error,omitempty"`
	LatencyMs int64  `json:"latency_ms"`
	Transport string `json:"transport,omitempty"` // "dmsg" or "http"
	// IP is the public ip:port of a DMSG server, sourced from the
	// dmsg-discovery all_servers endpoint (queried over dmsg). Only DMSG
	// servers carry an address; other services leave it empty and the UI
	// renders a hyphen.
	IP string `json:"ip,omitempty"`
}

// PortDetail type of port details
type PortDetail struct {
	Port string
	Type string
}

// DMSGServerInfo contains information about a connected DMSG server including latency.
type DMSGServerInfo struct {
	PK      cipher.PubKey `json:"pk"`
	Latency time.Duration `json:"latency"` // Round-trip latency via self-ping, 0 if not measured
	// Carrier is the transport the client↔server session runs on (tcp | ws | wt |
	// quic); Protocol is its human-readable label (e.g. "tcp", "wss",
	// "webtransport", "quic"). Empty when the session can't be matched. Lets the
	// UI show HOW each server was reached, not just that it was.
	Carrier  string `json:"carrier,omitempty"`
	Protocol string `json:"protocol,omitempty"`
	// Streams is the number of open mux streams on this session: 0 = idle (a
	// candidate for the idle-session reaper once it is over min_sessions), >0 =
	// carrying traffic, -1 = unmeasurable (quic, or no session matched this
	// server PK) which the reaper treats as busy. This is the same count
	// reapExcessIdleSessions decides on, so an operator seeing more sessions
	// than sessions_count can tell whether they are legitimately in use or the
	// reaper is not doing its job.
	Streams int `json:"streams"`
}

// DmsgClientSessions enumerates every dmsg client running inside the visor
// (main + embedded route setup node + embedded transport setup node) along
// with the dmsg server PKs each one currently has an active session to.
// Used by the `skywire cli dmsg sessions` command for operators investigating
// connectivity issues — the embedded RSN / TPS use SEPARATE dmsg keys and
// therefore SEPARATE sets of sessions from the main visor, so checking one
// doesn't tell you about the others.
type DmsgClientSessions struct {
	Main           *DmsgClientSessionInfo `json:"main,omitempty"`
	RouteSetup     *DmsgClientSessionInfo `json:"route_setup,omitempty"`
	TransportSetup *DmsgClientSessionInfo `json:"transport_setup,omitempty"`
}

// DmsgClientSessionInfo is one dmsg client's current session state.
type DmsgClientSessionInfo struct {
	PK      cipher.PubKey   `json:"pk"`
	Role    string          `json:"role"` // "main" | "route_setup" | "transport_setup"
	Count   int             `json:"count"`
	Servers []cipher.PubKey `json:"servers"`
	// Sessions carries the same servers plus the protocol each one was reached
	// over (tcp / ws / wss / webtransport / quic). Parallel to Servers, sorted
	// the same way, so existing Servers consumers keep working.
	Sessions []DmsgServerSession `json:"sessions,omitempty"`
	// RelayPeers are the visor's current relay nominees (peers it wants a
	// skynet-carried session through); RelayClients the peers currently
	// attached to THIS visor's relay acceptor.
	RelayPeers   []cipher.PubKey `json:"relay_peers,omitempty"`
	RelayClients []cipher.PubKey `json:"relay_clients,omitempty"`
}

// DmsgServerSession is one active dmsg-server session and the protocol the
// local client used to reach it.
type DmsgServerSession struct {
	PK cipher.PubKey `json:"pk"`
	// Carrier is the raw dmsg carrier: tcp | ws | wt | quic.
	Carrier string `json:"carrier"`
	// Protocol is a human-readable label distinguishing ws from wss:
	// tcp | ws | wss | webtransport | quic.
	Protocol string `json:"protocol"`
	// Address is the endpoint the carrier dialed (host:port or ws(s)://…).
	Address string `json:"address,omitempty"`
	// Streams is the number of open mux streams on this session (0 = idle,
	// -1 = unmeasurable/quic). Same count the idle-session reaper uses.
	Streams int `json:"streams"`
}

// DmsgHTTPRequest represents an HTTP request to be made over dmsg
type DmsgHTTPRequest struct {
	URL    string            `json:"url"`
	Method string            `json:"method"`
	Header map[string]string `json:"header,omitempty"`
	Body   []byte            `json:"body,omitempty"`
}

// DmsgHTTPResponse represents an HTTP response received over dmsg
type DmsgHTTPResponse struct {
	StatusCode int               `json:"status_code"`
	Status     string            `json:"status"`
	Header     map[string]string `json:"header,omitempty"`
	Body       []byte            `json:"body,omitempty"`
}

// SkynetHTTPRequest represents an HTTP request to make over skynet.
type SkynetHTTPRequest struct {
	PK     cipher.PubKey     `json:"pk"`
	Port   uint16            `json:"port"`
	Path   string            `json:"path"`
	Method string            `json:"method"`
	Header map[string]string `json:"header,omitempty"`
	Body   []byte            `json:"body,omitempty"`
}

// SkynetHTTPResponse represents an HTTP response received over skynet.
type SkynetHTTPResponse struct {
	StatusCode int               `json:"status_code"`
	Status     string            `json:"status"`
	Header     map[string]string `json:"header,omitempty"`
	Body       []byte            `json:"body,omitempty"`
}

// VisorSCPDirection enumerates the two scp transfer directions.
const (
	// VisorSCPUpload pushes a local file to the remote host's rootDir.
	VisorSCPUpload = "upload"
	// VisorSCPDownload pulls a file from the remote host's rootDir
	// into a local path.
	VisorSCPDownload = "download"
)

// VisorSCPTransport enumerates the transports the visor will dial
// the peer's dmsgscp host over. Both peers expose the host on dmsg
// AND skynet at the same port; the caller picks per-call.
const (
	// VisorSCPTransportDmsg dials peer:port over dmsg.
	VisorSCPTransportDmsg = "dmsg"
	// VisorSCPTransportSkynet dials peer:port over the skywire router
	// (appnet TypeSkynet). Routing through skynet keeps the bytes off
	// the dmsg overlay and lets the transfer ride whatever transports
	// the router picks for the route group.
	VisorSCPTransportSkynet = "skynet"
)

// VisorSCPRequest describes a peer-to-peer file transfer the visor
// should perform on the caller's behalf. The visor process opens the
// stream over the chosen transport (it has both dmsgC + appnet
// resolvers wired in, where a standalone CLI does not) and drives
// the dmsgscp wire protocol entirely inside the visor.
//
// Local files are read/written on the visor host's filesystem.
// Operators running the CLI as the same user as the visor process
// see the obvious "this is my $HOME" behavior.
type VisorSCPRequest struct {
	// RemotePK is the peer's visor PK. The peer must have a dmsgscp
	// host bound and the caller's PK on the host's whitelist.
	RemotePK cipher.PubKey `json:"remote_pk"`
	// Port is the peer's dmsgscp port (same for both transports —
	// dmsgscp.DefaultPort is 23).
	Port uint16 `json:"port"`
	// Direction is VisorSCPUpload or VisorSCPDownload.
	Direction string `json:"direction"`
	// LocalPath is the path on the visor host's filesystem. For an
	// upload it's the source; for a download it's the destination.
	LocalPath string `json:"local_path"`
	// RemotePath is the path relative to the peer's rootDir. For an
	// upload it's the destination; for a download it's the source.
	// Absolute paths and `..` components are rejected by the peer.
	RemotePath string `json:"remote_path"`
	// Transport is VisorSCPTransportDmsg or VisorSCPTransportSkynet.
	// Empty defaults to dmsg.
	Transport string `json:"transport,omitempty"`
	// Timeout bounds the whole transfer (dial + scp protocol +
	// payload). Zero falls back to a 5-minute default.
	Timeout time.Duration `json:"timeout,omitempty"`
}

// VisorCatMode enumerates the two cat operation modes.
const (
	// VisorCatModeDial opens an outbound stream to (RemotePK, Port)
	// over the chosen transport. The visor exposes the stream to the
	// CLI as a 127.0.0.1 loopback TCP listener — the CLI dials that
	// listener and the visor splices the two halves.
	VisorCatModeDial = "dial"
	// VisorCatModeListen opens a one-shot listener on Port over the
	// chosen transport (dmsg + skynet mirror), authorizes the first
	// inbound stream against the dmsgscp whitelist, and splices it
	// to a 127.0.0.1 loopback the CLI dials.
	VisorCatModeListen = "listen"
)

// VisorCatTransport enumerates the transports the visor will use
// for a VisorCat call — identical to VisorSCP's set so a caller's
// `--transport=skynet` keeps the same meaning across both commands.
const (
	// VisorCatTransportDmsg dials/listens over dmsg.
	VisorCatTransportDmsg = "dmsg"
	// VisorCatTransportSkynet dials/listens over the skywire router
	// (appnet.TypeSkynet).
	VisorCatTransportSkynet = "skynet"
)

// VisorCatRequest describes a stream-splice the visor should perform
// on the caller's behalf. The visor process opens the remote stream
// (dial or listen) over the chosen transport and exposes one end as
// a 127.0.0.1 loopback TCP listener — bytes never traverse the local
// RPC channel beyond the initial request + response.
type VisorCatRequest struct {
	// Mode is VisorCatModeDial or VisorCatModeListen.
	Mode string `json:"mode"`
	// RemotePK is the peer's visor PK. Required in dial mode;
	// ignored in listen mode (the listener accepts whatever PK the
	// dmsgscp whitelist authorizes).
	RemotePK cipher.PubKey `json:"remote_pk,omitempty"`
	// Port is the remote port to dial in dial mode, or the local
	// port to listen on in listen mode.
	Port uint16 `json:"port"`
	// Transport is VisorCatTransportDmsg or VisorCatTransportSkynet.
	// Empty defaults to dmsg.
	Transport string `json:"transport,omitempty"`
	// Timeout bounds the dial (dial mode) or the accept-wait (listen
	// mode) plus the splice. Zero defaults: 60s for dial, 5m for
	// listen.
	Timeout time.Duration `json:"timeout,omitempty"`
	// Routes requests N parallel mux routes for the skynet transport
	// in dial mode. The router opens N route-groups in parallel and
	// stripes writes across them with sequence numbers + resequencing
	// on reads — bytes remain ordered at the app layer. Ignored for
	// dmsg transport and for listen mode (listener accepts whatever
	// the peer dials). 0 or 1 = single route (default).
	Routes int `json:"routes,omitempty"`
}

// VisorCatResponse carries the 127.0.0.1 loopback address the CLI
// should dial. The visor's accept-and-splice goroutine waits on this
// listener; the CLI's dial completes the splice loop.
type VisorCatResponse struct {
	// LocalAddr is "127.0.0.1:PORT" the CLI dials to bridge stdio.
	LocalAddr string `json:"local_addr"`
}

// FetchCXOArgs identifies which CXO feed + path the caller wants the
// visor to read from its lazy-on-demand subscriber cache. Feed names
// are stable strings the CLI's URL→feed mapping table emits; adding
// a new feed means adding a case to the visor's FetchCXO switch and
// a row to the mapping table — nothing in between needs to know.
type FetchCXOArgs struct {
	// Feed is one of: "tpd-metrics", "tpd-uptime". (UT's standalone
	// uptime tracker is intentionally absent — that service is being
	// deprecated, so there's no point publishing it over CXO.)
	Feed string `json:"feed"`
	// Path is the TreeStore path inside the feed, e.g.
	// "metrics/days/7" or "uptimes/days/30".
	Path string `json:"path"`
}

// FetchCXOResult is the visor's reply for a FetchCXO probe. Hit is
// true when the subscriber returned a cached payload; a miss carries
// a Reason string ("not ready", "cooling down", "unknown feed", …)
// so the CLI's debug log has something concrete to print.
type FetchCXOResult struct {
	Hit        bool      `json:"hit"`
	Body       []byte    `json:"body,omitempty"`
	LastRootAt time.Time `json:"last_root_at,omitempty"`
	Reason     string    `json:"reason,omitempty"`
}

// CXORefreshArgs identifies which feed CXORefreshFeed should force a
// synchronous subscribe → first-Root → walk cycle on. Timeout caps
// how long the visor will wait for the first Root before giving up;
// zero means "use the manager's firstSyncTimeout default" (10s).
type CXORefreshArgs struct {
	Feed    string        `json:"feed"`
	Timeout time.Duration `json:"timeout,omitempty"`
}

const (
	// SupportedProtocolVersion is the app protocol version a visor reports.
	SupportedProtocolVersion = "0.1.0"
)

// AppSettings is the CLI's read of an app's live tuning knobs: the values the
// visor intends, the version they carry, and the version the app last reported
// having installed. Version != Applied is a change still in flight — the app
// installs it on its next pull (one tunnel.probe_interval, 5 s by default).
type AppSettings struct {
	AppName string           `json:"app_name"`
	Values  map[string]int64 `json:"values,omitempty"`
	// Text carries the LIST knobs (pool.exclude_pks, pool.require_tp_types),
	// whose payload is a comma-separated set of tokens rather than an int64.
	Text    map[string]string `json:"text,omitempty"`
	Version uint64            `json:"version"`
	Applied uint64            `json:"applied"`
}

// RuntimeLogsDelta is the diff-streaming response shape: only the
// entries newer than the caller's cursor, plus the new cursor and
// a count of entries the caller missed because they aged out of
// the ring buffer between calls (0 when keeping up).
type RuntimeLogsDelta struct {
	// Entries is the JSON-encoded log lines (each is one full JSON
	// object, same shape returned by RuntimeLogs's array elements).
	Entries []string `json:"entries"`
	// Latest is the highest log_line currently buffered. Pass it as
	// `since` on the next call to receive only newly-arrived entries.
	Latest int64 `json:"latest"`
	// Dropped tells the caller how many entries they missed since
	// their last cursor (because the buffer wrapped past them).
	// Zero when the caller is keeping up with the poll cadence.
	Dropped int64 `json:"dropped"`
}

// NodeHealth represents the health status of a setup node.
type NodeHealth struct {
	PK          cipher.PubKey `json:"pk"`
	Healthy     bool          `json:"healthy"`
	LastChecked time.Time     `json:"last_checked"`
	LastError   string        `json:"last_error,omitempty"`
	Latency     time.Duration `json:"latency_ms"`
}

type NetworkViewResponse = netview.Response

// ARSelfEntry is one transport-type-row of this visor's own AR registration:
// the IP:port it is reachable on according to the address-resolver.
//
// RemoteAddrV6 is the optional IPv6 counterpart of RemoteAddr, populated
// when this visor has bound the AR over an IPv6 HTTP client (#2719's
// secondary bind). Empty for v4-only deployments and on older AR servers
// that don't capture family — same backward-compat shape as the underlying
// addrresolver.VisorData (#2715).
type ARSelfEntry struct {
	Type         string   `json:"type"`                     // "stcpr", "sudph" or "wt"
	RemoteAddr   string   `json:"remote_addr,omitempty"`    // public IPv4 as AR sees the visor
	RemoteAddrV6 string   `json:"remote_addr_v6,omitempty"` // public IPv6 as AR sees the visor (#1525 Phase 4a)
	Port         string   `json:"port,omitempty"`           // listen port
	Addresses    []string `json:"addresses,omitempty"`      // local interface addresses the visor advertised
	// CertHash is the SHA-256 of the self-signed certificate a WebTransport
	// dialler must pin, lowercase hex. WT only — the other carriers have no
	// certificate. Empty from a visor too old to report it, which is why the
	// CLI omits the field rather than rendering "(none)": absent here means
	// "not said", and only a visor that knows about WT can distinguish that
	// from "not registered".
	CertHash string `json:"cert_hash,omitempty"`
}

// ARSelfRegistration is the visor's own AR record across transport types.
// One ARSelfEntry per transport the visor is currently registered for.
// Empty when the visor has not (yet) bound any transport with AR.
type ARSelfRegistration struct {
	Entries []ARSelfEntry `json:"entries,omitempty"`
	// Queried lists the transport types this visor actually checked, whether or
	// not it is registered for them. It exists so a caller can tell "asked, not
	// registered" from "never asked".
	//
	// Without it the two are the same silence, and the CLI cannot say which it
	// is looking at: a type missing from Entries might be unregistered, or the
	// answering visor might predate that type entirely. That is not
	// hypothetical here — `--via dmsg://<pk>` and `--rpc` point these commands
	// at other people's visors across the mesh, running whatever version they
	// are running. Locally it cannot happen, because the CLI and the visor are
	// the same binary.
	//
	// Empty from a visor too old to report it, which is itself the signal:
	// nothing can be concluded from a type's absence.
	Queried []string `json:"queried,omitempty"`
}

// Profile is the RPC-facing shape of a skychat profile.
//
// Re-declared rather than aliased for the same reason GroupDescriptor is:
// the RPC surface carries display-oriented additions the protocol type has
// no business knowing about — here, the public key it belongs to and a
// ready-to-render data URI, both of which are derived by the receiver.
type Profile struct {
	// PK is whose profile this is. Filled in by the visor from the key it
	// was asked about (or its own), never from the answer's body — a host
	// that could name the key in its own response could name someone
	// else's.
	PK cipher.PubKey `json:"pk"`

	Name string `json:"name,omitempty"`

	// Avatar is the encoded image, bounded by profile.MaxAvatarDim and
	// profile.MaxAvatarBytes on the way in. Carried as a
	// data URI rather than raw bytes because the only consumer is a
	// browser <img>, and building it here keeps the declared MIME type
	// the one the visor actually decoded.
	Avatar string `json:"avatar,omitempty"`

	Updated time.Time `json:"updated,omitzero"`

	// Address is the canonical skychat:// form of this key, so a caller
	// holding a profile can render, copy or QR-encode the address without
	// knowing the grammar.
	Address string `json:"address,omitempty"`
}

// ProfileSetArgs is the RPC input for ProfileSet.
type ProfileSetArgs struct {
	Name string `json:"name"`

	// Avatar is the new image as a data URI (what a browser's canvas
	// produces) or raw base64. Empty clears the avatar; see Clear for
	// removing the profile entirely.
	//
	// A string rather than []byte so the one caller that matters can pass
	// exactly what `canvas.toDataURL()` gave it, with no re-encoding step
	// to get wrong on the way.
	Avatar string `json:"avatar,omitempty"`

	// Clear removes the published profile entirely rather than saving an
	// empty one. Distinct from sending blank fields because "I have no
	// profile" and "my name is empty" want the same disk state and the
	// caller should not have to know that.
	Clear bool `json:"clear,omitempty"`
}

// ForwardedPort describes a single forwarded port with its metadata.
type ForwardedPort struct {
	// Port is the DMSG/skynet port exposed to the network.
	Port int `json:"port"`
	// LocalPort is the TCP port on localhost to forward to. If zero,
	// defaults to Port (same port number locally and remotely).
	LocalPort     int             `json:"local_port,omitempty"`
	Label         string          `json:"label,omitempty"`
	Description   string          `json:"description,omitempty"`
	ShowOnLanding bool            `json:"show_on_landing"`
	Whitelist     []cipher.PubKey `json:"whitelist,omitempty"` // empty = accessible to all authenticated peers
	Skynet        bool            `json:"skynet"`              // forward over skynet (sky-forwarding server)
	DMSG          bool            `json:"dmsg"`                // forward over DMSG (service registry)
	// UDP, when true, enables UDP-datagram semantics on this
	// forward rather than the default TCP-stream semantics.
	// Implementation rides DatagramRouteGroup (see
	// pkg/router/datagram_route_group.go and the faithful-UDP
	// design in #2607): faithful loss, no head-of-line blocking on
	// reorder, per-datagram AEAD. For DNS / NTP / VoIP / gaming —
	// anything where a late packet is worse than a lost one.
	//
	// Stage 4 of #2607: this field is recognized by the visor's
	// forwarded-port listener loop, which binds a local UDP socket
	// at EffectiveLocalPort() and pumps datagrams to/from the
	// peer-side DatagramRouteGroup. The route-setup machinery that
	// constructs the DatagramRouteGroup itself lives in stage 5's
	// app API.
	UDP bool `json:"udp,omitempty"`
	// ProxyAddr is an optional local address (e.g., "127.0.0.1:3000")
	// to reverse-proxy to. For port 80, this replaces the visor's
	// default landing page with content from the local service.
	ProxyAddr string `json:"proxy_addr,omitempty"`
	// PreserveHost controls the Host header on the request the
	// reverse-proxy emits to the backend. Currently effective on the
	// port-80 HTTP reverse-proxy (the only forward type that touches
	// HTTP headers; raw-TCP forwards don't see HTTP at all).
	//
	//   false (default): visor rewrites Host to the backend's address
	//                    (target.Host). Useful when the backend
	//                    validates Host against its listening address
	//                    — the historical behavior.
	//   true:            visor preserves whatever Host the incoming
	//                    request already carried (e.g. magnetosphere
	//                    .net after the skynetweb resolver's
	//                    subdomain rewrite). Required when the
	//                    backend (Caddy, nginx, traefik) dispatches
	//                    its virtual hosts by Host header.
	PreserveHost bool `json:"preserve_host,omitempty"`
	// InjectPK, when true, makes this forward HTTP-aware: instead of a
	// raw TCP splice, the visor terminates HTTP and reverse-proxies to
	// the backend, stamping the noise-authenticated caller into request
	// headers the backend can trust:
	//
	//   X-Skywire-Remote-PK   the caller's 66-hex public key (omitted if
	//                         the peer can't be identified)
	//   X-Skywire-Transport   "dmsg" or "skynet"
	//
	// Any client-supplied copies of those headers are stripped first, so
	// they cannot be spoofed over this path. This lets a forwarded website
	// do per-PK behavior (auth, personalization) that a raw splice can't,
	// because the backend otherwise never learns who is connecting.
	//
	// HTTP only — enabling it on a non-HTTP service breaks the stream. The
	// header is only trustworthy if the backend is reachable ONLY through
	// the visor (bind it to loopback); see docs/guides/skynet-website-auth.md.
	InjectPK bool `json:"inject_pk,omitempty"`
}

// EffectiveLocalPort returns the local TCP port to forward to.
// Returns LocalPort if set, otherwise Port.
func (fp *ForwardedPort) EffectiveLocalPort() int {
	if fp.LocalPort > 0 {
		return fp.LocalPort
	}
	return fp.Port
}

// DialTarget returns the address that incoming forwarded connections
// should be proxied to. ProxyAddr wins when set ("host:port" or
// "ip:port"); otherwise it falls back to localhost:EffectiveLocalPort.
func (fp *ForwardedPort) DialTarget() string {
	if fp.ProxyAddr != "" {
		return fp.ProxyAddr
	}
	return fmt.Sprintf("localhost:%d", fp.EffectiveLocalPort())
}

// AddPtyWhitelistIn carries the PKs to merge into the visor's shared
// peer whitelist (a hypervisor pushing its own hypervisors).
type AddPtyWhitelistIn struct {
	PKs []cipher.PubKey
}

// SetHypervisorAuthIn is the argument of SetHypervisorAuth.
type SetHypervisorAuthIn struct {
	Enable  bool
	Persist bool
}

// StateSnapshotReq is the argument to StateSnapshotProjected: the subtree keys
// (see the StateSelect* constants) to build. Empty builds the full snapshot.
type StateSnapshotReq struct {
	Fields []string
}

// ConfigFieldChange reports one field's before/after from SetConfigFields.
type ConfigFieldChange struct {
	// Path is the dotted config path as the caller supplied it.
	Path string `json:"path"`
	// Old and New are the field's JSON values before and after.
	// Old is null when the path addressed an absent map key.
	Old json.RawMessage `json:"old"`
	New json.RawMessage `json:"new"`
	// Live is true when a running subsystem took the value; false
	// means it is on disk and applies at the next visor start.
	Live bool `json:"live"`
}

// String renders one change the way `config set` prints it.
func (c ConfigFieldChange) String() string {
	state := "restart-required"
	if c.Live {
		state = "live"
	}
	return fmt.Sprintf("%s: %s -> %s (%s)", c.Path, string(c.Old), string(c.New), state)
}
