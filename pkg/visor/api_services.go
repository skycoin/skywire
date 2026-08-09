// Package visor pkg/visor/api_services.go c3-vis-core
// api_services.go contains service discovery, health, and configuration API methods.
package visor

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/skycoin/skywire/deployment"
	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/pty"
	"github.com/skycoin/skywire/pkg/servicedisc"
	"github.com/skycoin/skywire/pkg/skyenv"
	"github.com/skycoin/skywire/pkg/visor/visorconfig"
	"github.com/skycoin/skywire/pkg/visor/visorcore"
)

// ServiceHealth checks the health of all configured deployment services.
//
// Each service URL is probed over the same transport the visor is configured
// to use: URLs with a dmsg:// scheme go through the visor's DMSG client,
// http/https URLs go through the default HTTP transport. This matches the
// visor's actual traffic path, so the dashboard reflects the reachability
// the visor itself experiences (not a separate probe path).
//
// Entries are returned in a stable order (HTTP/DMSG services first, then
// DMSG servers sorted by PK, then route setup nodes, then transport setup
// nodes). Network checks within each group run in parallel so the total
// response time is dominated by the slowest single check.
//
// The following are probed:
//   - Services: Config Service, Transport Discovery, DMSG Discovery,
//     Address Resolver, Route Finder, Service Discovery, Uptime Tracker
//     (each via GET {base}/health, dmsg:// or http://)
//   - DMSG servers: one entry per server the visor currently has an
//     active session with; latency comes from the visor's measured
//     dmsg latency table
//   - Route Setup Nodes: one entry per PK in EffectiveRouteSetupNodes;
//     health is determined by whether the DMSG discovery entry has a
//     non-empty DelegatedServers list (RSN/TPS don't serve HTTP /health
//     over DMSG, so discovery registration is the health signal)
//   - Transport Setup Nodes: same scheme as RSN
func (v *Visor) ServiceHealth() ([]ServiceHealthEntry, error) {
	// ---------- HTTP / DMSG services (ordered) ----------
	type svcURLs struct {
		httpURL, dmsgURL string
	}
	httpServices := []struct {
		name string
		urls svcURLs
	}{
		{"Config Service", svcURLs{v.confServiceHTTP(), v.confServiceDmsg()}},
		{"Transport Discovery", svcURLs{v.conf.Transport.Discovery, v.conf.Transport.DiscoveryDmsg}},
		{"DMSG Discovery", svcURLs{v.conf.Dmsg.Discovery, v.conf.Dmsg.DiscoveryDmsg}},
		{"Address Resolver", svcURLs{v.conf.Transport.AddressResolver, v.conf.Transport.AddressResolverDmsg}},
		{"Route Finder", svcURLs{v.conf.Routing.RouteFinder, v.conf.Routing.RouteFinderDmsg}},
		{"Service Discovery", svcURLs{v.conf.Launcher.ServiceDisc, v.conf.Launcher.ServiceDiscDmsg}},
	}
	if v.conf.UptimeTracker != nil {
		httpServices = append(httpServices, struct {
			name string
			urls svcURLs
		}{"Uptime Tracker", svcURLs{v.conf.UptimeTracker.Addr, v.conf.UptimeTracker.AddrDmsg}})
	}

	// Probe every service over dmsg only — plain HTTP to deployment services is
	// no longer supported. v.dmsgHTTP connects through a direct client with
	// pre-loaded entries for all deployment services (no discovery lookup). The
	// dmsg URL may live in either field: the dmsg-only default config stores it
	// in the "http" field (e.g. service_discovery = "dmsg://..."), legacy dual
	// configs use the *_dmsg field — pick whichever holds the dmsg:// URL.
	var dmsgClient *http.Client
	select {
	case <-v.dmsgHTTPReady:
		dmsgClient = v.dmsgHTTP
	default:
		// Not ready yet; services report N/A this poll.
	}

	httpResults := make([]ServiceHealthEntry, len(httpServices))
	var wg sync.WaitGroup
	for i, svc := range httpServices {
		dmsgURL := svc.urls.dmsgURL
		if !strings.HasPrefix(dmsgURL, "dmsg://") {
			dmsgURL = svc.urls.httpURL
		}
		if !strings.HasPrefix(dmsgURL, "dmsg://") || dmsgClient == nil {
			httpResults[i] = ServiceHealthEntry{Name: svc.name, Status: "N/A"}
			continue
		}
		wg.Add(1)
		go func(i int, name, url string) {
			defer wg.Done()
			httpResults[i] = doHealthProbe(dmsgClient, name, url, "dmsg")
		}(i, svc.name, dmsgURL)
	}
	wg.Wait()

	results := make([]ServiceHealthEntry, 0, len(httpResults)+16)
	for _, e := range httpResults {
		if e.Name != "" {
			results = append(results, e)
		}
	}

	// ---------- DMSG servers ----------
	// Pass the (possibly nil) dmsg-HTTP client so each server's version is
	// fetched over the same discovery-routed path as the services above.
	if v.dmsgC != nil {
		results = append(results, v.dmsgServerHealth(dmsgClient)...)
	}

	return results, nil
}

// doHealthProbe performs a single GET {baseURL}/health and populates a ServiceHealthEntry.
func doHealthProbe(client *http.Client, name, baseURL, transport string) ServiceHealthEntry { //nolint:unparam
	entry := ServiceHealthEntry{Name: name, URL: baseURL, Transport: transport}

	reqURL := strings.TrimSuffix(baseURL, "/") + "/health"
	start := time.Now()
	resp, err := client.Get(reqURL) //nolint:gosec
	entry.LatencyMs = time.Since(start).Milliseconds()

	if err != nil {
		entry.Status = "DOWN"
		entry.Error = err.Error()
		return entry
	}
	body, _ := io.ReadAll(resp.Body) //nolint:errcheck,gosec
	resp.Body.Close()                //nolint:errcheck,gosec

	if resp.StatusCode != http.StatusOK {
		entry.Status = fmt.Sprintf("ERROR(%d)", resp.StatusCode)
		return entry
	}

	var health map[string]interface{}
	if json.Unmarshal(body, &health) == nil {
		if bi, ok := health["build_info"].(map[string]interface{}); ok {
			if ver, ok := bi["version"].(string); ok {
				entry.Version = ver
			}
		}
		if entry.Version == "" {
			if ver, ok := health["version"].(string); ok {
				entry.Version = ver
			}
		}
	}
	entry.Status = "OK"
	return entry
}

// confServiceHTTP returns the conf service HTTP URL from config, falling
// back to the embedded deployment default.
func (v *Visor) confServiceHTTP() string {
	if v.conf.ConfService != "" {
		return v.conf.ConfService
	}
	return deployment.ProdConf.Conf
}

func (v *Visor) confServiceDmsg() string {
	return visorcore.ResolveServices(v.conf).ConfDmsg
}

// dmsgServerHealth returns one ServiceHealthEntry per DMSG server the
// visor currently holds an active session with. Latency is the last
// measured ping RTT (0 if unmeasured). Entries are sorted by PK so the
// UI order remains stable across polls (DMSGServers() sorts by latency
// which flips between samples and causes visible reordering).
func (v *Visor) dmsgServerHealth(httpClient *http.Client) []ServiceHealthEntry {
	servers, err := v.DMSGServers()
	if err != nil || len(servers) == 0 {
		return nil
	}
	sort.Slice(servers, func(i, j int) bool {
		return servers[i].PK.String() < servers[j].PK.String()
	})

	// Map each server PK to its public ip:port from the dmsg-discovery
	// all_servers cache (refreshed over dmsg). Only DMSG servers carry an
	// address; the UI shows it in the IP column and a hyphen elsewhere.
	addrByPK := map[cipher.PubKey]string{}
	if v.dmsgServersCache != nil {
		for _, e := range v.dmsgServersCache.All() {
			if e != nil && e.Server != nil && e.Server.Address != "" {
				addrByPK[e.Static] = e.Server.Address
			}
		}
	}

	out := make([]ServiceHealthEntry, len(servers))
	var wg sync.WaitGroup
	for i, s := range servers {
		latStr := s.Latency.Milliseconds()
		out[i] = ServiceHealthEntry{
			Name:      "DMSG Server",
			URL:       "dmsg://" + s.PK.String() + ":80",
			Status:    "OK",
			Transport: "dmsg",
			LatencyMs: latStr,
			IP:        addrByPK[s.PK],
		}
		// Fetch the version from the server's /health endpoint over dmsg via
		// the SAME discovery-routed dmsg-HTTP client the other services use —
		// NOT a stream pinned to this server's own session. A dmsg server
		// serves /health on its transit dmsg client (dmsg port 80, a normal
		// client entry in discovery); dialing the relay server's own PK
		// through the session with THAT server finds no local client session
		// for the PK and fails, which is why the VERSION column was empty.
		// doHealthProbe applies the same build_info.version extraction as the
		// deployment-service rows.
		if httpClient != nil {
			wg.Add(1)
			go func(idx int, serverPK cipher.PubKey) {
				defer wg.Done()
				probe := doHealthProbe(httpClient, "DMSG Server", "dmsg://"+serverPK.String()+":80", "dmsg")
				if probe.Version != "" {
					out[idx].Version = probe.Version
				}
			}(i, s.PK)
		}
	}
	wg.Wait()
	return out
}

// serviceHop is one transport attempt for a service fetch: a base URL and
// whether it is reached over DMSG-HTTP or plain HTTP.
type serviceHop struct {
	baseURL string
}

// serviceFetchOrder returns the dmsg endpoints to fetch service data from.
// Deployment services are dmsg-only: plain HTTP is no longer supported, so only
// dmsg:// URLs are used and clearnet URLs are ignored. The dmsg URL may live in
// either argument — the dmsg-only default config stores it in the "http" field
// (e.g. service_discovery = "dmsg://..."), legacy dual configs use the *_dmsg
// field — so both are checked and any dmsg:// one is taken.
func serviceFetchOrder(httpURL, dmsgURL string) []serviceHop {
	var order []serviceHop
	for _, u := range []string{dmsgURL, httpURL} {
		if strings.HasPrefix(u, "dmsg://") {
			order = append(order, serviceHop{baseURL: u})
		}
	}
	return order
}

// FetchServiceData fetches data from a deployment service endpoint via the visor's
// configured URLs. service is one of: tpd, ut, sd, ar, rf, dmsgd.
// path is the URL path (e.g., "/all-transports/stats").
//
// DMSG-HTTP is preferred whenever the service has a dmsg URL configured — it is
// the default service transport and a dmsg-only visor has no HTTP URL at all.
// Plain HTTP is used only as the dual-config fallback or the http-only path.
func (v *Visor) FetchServiceData(service, path string) ([]byte, error) {
	var httpURL, dmsgURL string
	switch service {
	case "tpd":
		httpURL, dmsgURL = v.conf.Transport.Discovery, v.conf.Transport.DiscoveryDmsg
	case "ut":
		if v.conf.UptimeTracker != nil {
			httpURL, dmsgURL = v.conf.UptimeTracker.Addr, v.conf.UptimeTracker.AddrDmsg
		}
	case "sd":
		httpURL, dmsgURL = v.conf.Launcher.ServiceDisc, v.conf.Launcher.ServiceDiscDmsg
	case "ar":
		httpURL, dmsgURL = v.conf.Transport.AddressResolver, v.conf.Transport.AddressResolverDmsg
	case "rf":
		httpURL, dmsgURL = v.conf.Routing.RouteFinder, v.conf.Routing.RouteFinderDmsg
	case "dmsgd":
		httpURL = v.conf.Dmsg.Discovery
	default:
		return nil, fmt.Errorf("unknown service: %s (valid: tpd, ut, sd, ar, rf, dmsgd)", service)
	}

	order := serviceFetchOrder(httpURL, dmsgURL)
	if len(order) == 0 {
		return nil, fmt.Errorf("service %s not configured", service)
	}

	var lastErr error
	for _, hop := range order {
		url := strings.TrimSuffix(hop.baseURL, "/") + path
		body, err := v.fetchServiceDataDmsg(url)
		if err == nil {
			return body, nil
		}
		lastErr = err
	}
	return nil, lastErr
}

// fetchServiceDataDmsg performs a GET over DMSG-HTTP using the visor's
// authoritative dmsg client.
func (v *Visor) fetchServiceDataDmsg(url string) ([]byte, error) {
	resp, err := v.DmsgHTTP(DmsgHTTPRequest{URL: url, Method: http.MethodGet})
	if err != nil {
		return nil, fmt.Errorf("fetch %s over dmsg: %w", url, err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("service returned %d over dmsg: %s", resp.StatusCode, string(resp.Body))
	}
	return resp.Body, nil
}

// servicesFromDmsg queries service discovery for service entries matching the
// given type over dmsg (the CXO snapshot is the primary path; this is the
// dmsg-HTTP fallback). Deployment services are dmsg-only — no plain HTTP.
func (v *Visor) servicesFromDmsg(serviceType, version, country string) ([]servicedisc.Service, error) {
	sdURL := v.conf.Launcher.ServiceDiscDmsg
	if sdURL == "" {
		sdURL = v.conf.Launcher.ServiceDisc
	}
	if !strings.HasPrefix(sdURL, "dmsg://") {
		return nil, fmt.Errorf("service discovery is not a dmsg:// URL")
	}
	httpC, err := getHTTPClient(context.Background(), v, sdURL)
	if err != nil {
		return nil, err
	}
	log := logging.MustGetLogger("servicedisc")
	vLog := logging.NewMasterLogger()
	vLog.SetLevel(logrus.InfoLevel)
	sdClient := servicedisc.NewClient(log, vLog, servicedisc.Config{
		Type:          serviceType,
		PK:            v.conf.PK,
		SK:            v.conf.SK,
		DiscAddr:      sdURL,
		DisplayNodeIP: v.conf.Launcher.DisplayNodeIP,
	}, httpC, "")
	return sdClient.Services(context.Background(), 0, version, country)
}

// servicesFromCXO walks the SD services snapshot under
// services/<serviceType>/<pk>/entry and rebuilds a []Service
// honoring optional version + country filters. Returns ok=false when
// the manager isn't installed, the snapshot is empty, or every leaf
// failed to parse — caller falls through to servicesFromDmsg.
func (v *Visor) servicesFromCXO(serviceType, version, country string) ([]servicedisc.Service, bool) {
	mgr := v.CXOSubMgr()
	if mgr == nil {
		return nil, false
	}
	mgr.AcquireFor(TabCLIServices)
	defer mgr.ReleaseFor(TabCLIServices)

	prefix := "services/" + serviceType + "/"
	var services []servicedisc.Service
	mgr.Walk(FeedSDServices, prefix, func(path string, body []byte) bool {
		if !strings.HasSuffix(path, "/entry") {
			return true
		}
		var svc servicedisc.Service
		if err := json.Unmarshal(body, &svc); err != nil {
			return true
		}
		if version != "" && svc.Version != version {
			return true
		}
		if country != "" {
			if svc.Geo == nil || svc.Geo.Country != country {
				return true
			}
		}
		services = append(services, svc)
		return true
	})
	if len(services) == 0 {
		return nil, false
	}
	return services, true
}

// VPNServers gets available public VPN servers — CXO snapshot first,
// HTTP service-discovery fallback.
func (v *Visor) VPNServers(version, country string) ([]servicedisc.Service, error) {
	if services, ok := v.servicesFromCXO(servicedisc.ServiceTypeVPN, version, country); ok {
		return services, nil
	}
	return v.servicesFromDmsg(servicedisc.ServiceTypeVPN, version, country)
}

// ProxyServers gets available proxy servers — CXO snapshot first,
// HTTP service-discovery fallback.
func (v *Visor) ProxyServers(version, country string) ([]servicedisc.Service, error) {
	if services, ok := v.servicesFromCXO(servicedisc.ServiceTypeProxy, version, country); ok {
		return services, nil
	}
	return v.servicesFromDmsg(servicedisc.ServiceTypeProxy, version, country)
}

// PublicVisors gets available public visors — CXO snapshot first,
// HTTP service-discovery fallback.
func (v *Visor) PublicVisors(version, country string) ([]servicedisc.Service, error) {
	if services, ok := v.servicesFromCXO(servicedisc.ServiceTypeVisor, version, country); ok {
		return services, nil
	}
	return v.servicesFromDmsg(servicedisc.ServiceTypeVisor, version, country)
}

// DeregisterService deregisters the specified public keys from service discovery.
// This requires the visor's public key to be whitelisted as a network monitor in the service discovery.
// serviceType must be one of: "vpn", "visor", "skysocks" (or "proxy")
func (v *Visor) DeregisterService(pks []cipher.PubKey, serviceType string) error {
	if len(pks) == 0 {
		return fmt.Errorf("no public keys provided")
	}

	// Normalize service type
	sType := serviceType
	if sType == "proxy" {
		sType = "skysocks"
	}
	if sType != "vpn" && sType != "visor" && sType != "skysocks" {
		return fmt.Errorf("invalid service type %q: must be vpn, visor, or skysocks (proxy)", serviceType)
	}

	// Sign the visor's public key with its secret key (network monitor authentication)
	nmSign, err := cipher.SignPayload([]byte(v.conf.PK.Hex()), v.conf.SK)
	if err != nil {
		return fmt.Errorf("failed to sign payload: %w", err)
	}

	// Build the URL
	sdURL := v.conf.Launcher.ServiceDisc
	reqURL := fmt.Sprintf("%s/api/services/deregister/%s", sdURL, sType)

	// Convert public keys to hex strings
	pkStrings := make([]string, len(pks))
	for i, pk := range pks {
		pkStrings[i] = pk.Hex()
	}

	// Marshal the public keys to JSON
	jsonData, err := json.Marshal(pkStrings)
	if err != nil {
		return fmt.Errorf("failed to marshal public keys: %w", err)
	}

	// Create the request
	req, err := http.NewRequest(http.MethodDelete, reqURL, bytes.NewReader(jsonData))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	// Set headers
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("NM-PK", v.conf.PK.Hex())
	req.Header.Set("NM-Sign", nmSign.Hex())

	// Send the request
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck

	// Check response
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body) //nolint:errcheck
		return fmt.Errorf("server returned %d: %s", resp.StatusCode, string(body))
	}

	v.log.Infof("Successfully deregistered %d key(s) from service type %s", len(pks), sType)
	return nil
}

// RemoteVisors return list of connected remote visors
func (v *Visor) RemoteVisors() ([]string, error) {
	var visors []string
	for _, conn := range v.remoteVisors {
		visors = append(visors, conn.Addr.PK.String())
	}
	return visors, nil
}

// ManagedVisors implements logserver.RelatedNodesProvider. It returns the PKs
// of the visors this node manages as a hypervisor — the same connected set as
// `hv ls`. Empty when this node is not acting as a hypervisor (v.conf.Hypervisor
// nil), so the landing page only shows the managed-visors section for a node
// that actually IS a hypervisor. Reads v.remoteVisors lock-free, matching the
// existing RemoteVisors() accessor.
func (v *Visor) ManagedVisors() []cipher.PubKey {
	if v == nil || v.conf == nil || v.conf.Hypervisor == nil {
		return nil
	}
	out := make([]cipher.PubKey, 0, len(v.remoteVisors))
	for _, conn := range v.remoteVisors {
		out = append(out, conn.Addr.PK)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Hex() < out[j].Hex() })
	return out
}

// ConfiguredHypervisors implements logserver.RelatedNodesProvider. It returns
// the PKs of the hypervisors this visor is configured to be managed by
// (v.conf.Hypervisors).
func (v *Visor) ConfiguredHypervisors() []cipher.PubKey {
	if v == nil || v.conf == nil {
		return nil
	}
	out := make([]cipher.PubKey, 0, len(v.conf.Hypervisors))
	out = append(out, v.conf.Hypervisors...)
	sort.Slice(out, func(i, j int) bool { return out[i].Hex() < out[j].Hex() })
	return out
}

// DmsgPtyExec runs a one-shot command on the remote visor identified
// by args.RemotePK using this visor's embedded dmsgpty host. Skips
// the host's CLI control listener (the unix socket / TCP loopback
// that the standalone dmsgpty-cli connects to) — the caller already
// holds an authenticated RPC connection to this visor, so the
// permission gate is the visor RPC, not the dmsgpty CLI socket.
//
// Trust model upstream of this is the visor RPC's auth (whoever can
// reach :3435). Trust model downstream is unchanged: the remote
// dmsgpty host enforces its whitelist on the dmsg stream this visor
// opens; the remote sees this visor's PK as the peer.
func (v *Visor) DmsgPtyExec(args DmsgPtyExecArgs) (*pty.CommandExecResult, error) {
	if v.dmsgPty == nil {
		return nil, fmt.Errorf("dmsgpty: not initialized on this visor")
	}
	if args.RemotePK.Null() {
		return nil, fmt.Errorf("dmsgpty: remote_pk required")
	}
	// Local-privesc gate (Pty.AllowRPCExec, default OFF) applies ONLY to exec
	// targeting THIS visor's own PK: that loops back to the local dmsgpty host and
	// yields a shell on THIS machine as the dmsgpty-host identity (root on a root
	// deployment), so any local process reaching the visor RPC socket could
	// self-escalate. Exec targeting a REMOTE visor is the legitimate hypervisor-
	// control path (`cli pty exec <remote-pk>`) and is gated by the REMOTE's own
	// dmsgpty whitelist — the target decides whether to trust this visor's PK — so it
	// is always allowed. (Earlier this gate blanket-disabled the RPC method, which
	// also broke a hypervisor driving the pty of the visors it manages.)
	if args.RemotePK == v.conf.PK && (v.conf.Pty == nil || !v.conf.Pty.AllowRPCExec) {
		return nil, fmt.Errorf("dmsgpty: RPC-initiated exec on this visor's OWN pk is disabled (local-privesc guard); set pty.allow_rpc_exec=true to allow self-exec. Remote targets are unaffected")
	}
	req := args.Req
	// Bound the whole call: Req.TimeoutMS covers the remote command's
	// execution; we add a 15s dial/handshake budget on top so an
	// unreachable target (stale dmsg discovery, downed peer, etc.)
	// doesn't hang this RPC goroutine indefinitely. Without the bound,
	// the dmsg client's dial blocks in a syscall that doesn't unwind
	// on RPC-side cancellation, leaking a goroutine per attempt and
	// preventing the caller's `timeout` wrapper from ever returning.
	const dialBudget = 15 * time.Second
	callTimeout := time.Duration(req.TimeoutMS)*time.Millisecond + dialBudget
	ctx, cancel := context.WithTimeout(context.Background(), callTimeout)
	defer cancel()

	// Scheme selects a specific dialer over the Host's default
	// MultiDialer chain. Useful when skynet's dial hangs without
	// honoring ctx and the operator knows dmsg is reachable —
	// passing Scheme="dmsg" skips skynet entirely. Empty keeps the
	// default chain (skynet first, dmsg fallback).
	switch args.Scheme {
	case "":
		return v.dmsgPty.ExecRemote(ctx, args.RemotePK, args.RemotePort, &req)
	case "dmsg":
		if v.dmsgC == nil {
			return nil, fmt.Errorf("dmsgpty: dmsg scheme requested but dmsg client not initialized")
		}
		return v.dmsgPty.ExecRemoteVia(ctx, pty.NewDmsgDialer(v.dmsgC), args.RemotePK, args.RemotePort, &req)
	case "skynet":
		return v.dmsgPty.ExecRemoteVia(ctx, skywireDialer{ensureTransport: v.ensureFastTransport}, args.RemotePK, args.RemotePort, &req)
	default:
		return nil, fmt.Errorf("dmsgpty: unknown scheme %q (want \"\", \"dmsg\", or \"skynet\")", args.Scheme)
	}
}

// Ports return list of all ports used by visor services and apps
func (v *Visor) Ports() (map[string]PortDetail, error) {
	ctx := context.Background()
	var ports = make(map[string]PortDetail)

	// Every optional section really is optional here: mobile-profile configs
	// ship without pty/skywire-tcp and with an empty cli_addr, and this
	// endpoint must not panic on them.
	if v.conf.Hypervisor != nil {
		ports["hypervisor"] = PortDetail{Port: addrPort(v.conf.Hypervisor.HTTPAddr), Type: "TCP"}
	}

	if v.conf.Pty != nil {
		ports["dmsg_pty"] = PortDetail{Port: fmt.Sprint(v.conf.Pty.DmsgPort), Type: "DMSG"}
	}
	if v.conf.CLIAddr != "" {
		ports["cli_addr"] = PortDetail{Port: addrPort(v.conf.CLIAddr), Type: "TCP"}
	}
	if v.conf.Launcher != nil {
		ports["proc_addr"] = PortDetail{Port: addrPort(v.conf.Launcher.ServerAddr), Type: "TCP"}
	}
	if v.conf.STCP != nil {
		ports["stcp_addr"] = PortDetail{Port: addrPort(v.conf.STCP.ListeningAddress), Type: "TCP"}
	}

	if v.arClient != nil {
		sudphPort := v.arClient.Addresses(ctx)
		if sudphPort != "" {
			ports["sudph"] = PortDetail{Port: sudphPort, Type: "UDP"}
		}
	}
	if v.stun.client != nil {
		if v.stun.client.PublicIP != nil {
			ports["public_visor"] = PortDetail{Port: fmt.Sprint(v.stun.client.PublicIP.Port()), Type: "TCP"}
		}
	}
	if v.dmsgC != nil {
		dmsgSessions := v.dmsgC.AllSessions()
		for i, session := range dmsgSessions {
			ports[fmt.Sprintf("dmsg_session_%d", i)] = PortDetail{Port: strings.Split(session.LocalTCPAddr().String(), ":")[1], Type: "TCP"}
		}

		dmsgStreams := v.dmsgC.AllStreams()
		for i, stream := range dmsgStreams {
			ports[fmt.Sprintf("dmsg_stream_%d", i)] = PortDetail{Port: strings.Split(stream.LocalAddr().String(), ":")[1], Type: "DMSG"}
		}
	}
	if v.procM != nil {
		apps, _ := v.Apps() //nolint:errcheck,gosec
		for _, app := range apps {
			port, err := v.procM.GetAppPort(app.Name)
			if err == nil {
				ports[app.Name] = PortDetail{Port: fmt.Sprint(port), Type: "SKYNET"}

				switch app.Name {
				case "skysocks_client":
					ports["skysocks_client_addr"] = PortDetail{Port: fmt.Sprint(strings.Split(skyenv.SkysocksClientAddr, ":")[1]), Type: "TCP"}
				case "skychat":
					ports["skychat_addr"] = PortDetail{Port: fmt.Sprint(strings.Split(skyenv.SkychatAddr, ":")[1]), Type: "TCP"}
				}
			}
		}
	}
	return ports, nil
}

// addrPort returns the port part of a host:port address, or "" when the
// address has none — never panics on empty/portless input.
func addrPort(addr string) string {
	if i := strings.LastIndex(addr, ":"); i >= 0 {
		return addr[i+1:]
	}
	return ""
}

// SetLogRotationInterval sets log_rotation_interval config of visor
func (v *Visor) SetLogRotationInterval(d visorconfig.Duration) error {
	return v.conf.UpdateLogRotationInterval(d)
}

// GetLogRotationInterval gets log_rotation_interval config of visor
func (v *Visor) GetLogRotationInterval() (visorconfig.Duration, error) {
	return v.conf.GetLogRotationInterval()
}

// SetPublicAutoconnect sets public_autoconnect config of visor
func (v *Visor) SetPublicAutoconnect(pAc bool) error {
	return v.conf.UpdatePublicAutoconnect(pAc)
}

// SetIsPublic sets is_public config of visor and flushes the config.
func (v *Visor) SetIsPublic(isPublic bool) error {
	v.conf.IsPublic = isPublic
	return v.conf.Flush()
}

// GetIsPublic returns the current is_public config setting.
func (v *Visor) GetIsPublic() bool {
	return v.conf.IsPublic
}

// GetRuntimeConfig returns the visor's running config as JSON bytes with the
// secret key redacted — it never leaves the process via this view (matching the
// browser wasm-visor). The PK is kept but is not editable on save; see
// SetRuntimeConfig, which preserves and re-injects the real SK.
func (v *Visor) GetRuntimeConfig() ([]byte, error) {
	b, err := json.MarshalIndent(v.conf, "", "  ")
	if err != nil {
		return nil, err
	}
	return redactTopLevelSK(b), nil
}

// GetConfigPath returns the filesystem path the visor loaded its config
// from. Returns "stdin" when the config was read from stdin or supplied
// via --config-arg; returns an empty string if the path was never set.
func (v *Visor) GetConfigPath() (string, error) {
	return visorconfig.VisorConfigFile, nil
}

// PublicAutoconnectStatus returns whether public autoconnect is currently running
func (v *Visor) PublicAutoconnectStatus() (bool, error) {
	return v.IsPublicAutoconnectRunning(), nil
}
