// api_services.go contains service discovery, health, and configuration API methods.
package visor

import (
	"bufio"
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
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/dmsg/dmsgpty"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/servicedisc"
	"github.com/skycoin/skywire/pkg/skyenv"
	"github.com/skycoin/skywire/pkg/visor/visorconfig"
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

	// Use the visor's direct DMSG HTTP client first (v.dmsgHTTP). It
	// connects through a direct client with pre-loaded entries for all
	// deployment services — no discovery lookup needed. Falls back to
	// HTTP if the direct client isn't ready or the probe fails.
	httpClient := &http.Client{Timeout: 10 * time.Second}
	var dmsgClient *http.Client
	select {
	case <-v.dmsgHTTPReady:
		dmsgClient = v.dmsgHTTP
	default:
		// Not ready yet; will use HTTP only.
	}

	httpResults := make([]ServiceHealthEntry, len(httpServices))
	var wg sync.WaitGroup
	for i, svc := range httpServices {
		if svc.urls.httpURL == "" && svc.urls.dmsgURL == "" {
			httpResults[i] = ServiceHealthEntry{Name: svc.name, Status: "N/A"}
			continue
		}
		wg.Add(1)
		go func(i int, name string, urls svcURLs) {
			defer wg.Done()
			// Try DMSG first if a dmsg URL is configured.
			if urls.dmsgURL != "" && dmsgClient != nil {
				entry := doHealthProbe(dmsgClient, name, urls.dmsgURL, "dmsg")
				if entry.Status == "OK" {
					httpResults[i] = entry
					return
				}
			}
			// Fall back to HTTP.
			if urls.httpURL != "" {
				httpResults[i] = doHealthProbe(httpClient, name, urls.httpURL, "http")
				return
			}
			httpResults[i] = ServiceHealthEntry{Name: name, Status: "N/A"}
		}(i, svc.name, svc.urls)
	}
	wg.Wait()

	results := make([]ServiceHealthEntry, 0, len(httpResults)+16)
	for _, e := range httpResults {
		if e.Name != "" {
			results = append(results, e)
		}
	}

	// ---------- DMSG servers (from session data, no HTTP probe) ----------
	if v.dmsgC != nil {
		results = append(results, v.dmsgServerHealth(nil)...)
	}

	return results, nil
}

// doHealthProbe performs a single GET {baseURL}/health and populates a ServiceHealthEntry.
func doHealthProbe(client *http.Client, name, baseURL, transport string) ServiceHealthEntry {
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
	if v.conf.ConfServiceDmsg != "" {
		return v.conf.ConfServiceDmsg
	}
	return deployment.Prod.ConfDmsg
}

// dmsgServerHealth returns one ServiceHealthEntry per DMSG server the
// visor currently holds an active session with. Latency is the last
// measured ping RTT (0 if unmeasured). Entries are sorted by PK so the
// UI order remains stable across polls (DMSGServers() sorts by latency
// which flips between samples and causes visible reordering).
func (v *Visor) dmsgServerHealth(_ *http.Client) []ServiceHealthEntry {
	servers, err := v.DMSGServers()
	if err != nil || len(servers) == 0 {
		return nil
	}
	sort.Slice(servers, func(i, j int) bool {
		return servers[i].PK.String() < servers[j].PK.String()
	})

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
		}
		// Probe /health via the existing session to get the version.
		if v.dmsgC != nil {
			wg.Add(1)
			go func(idx int, serverPK cipher.PubKey) {
				defer wg.Done()
				ses, ok := v.dmsgC.Session(serverPK)
				if !ok {
					return
				}
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				stream, err := ses.DialStream(ctx, dmsg.Addr{PK: serverPK, Port: 80})
				if err != nil {
					return
				}
				defer stream.Close() //nolint:errcheck
				// Send HTTP request on the stream.
				req, _ := http.NewRequestWithContext(ctx, "GET", "http://"+serverPK.String()+":80/health", nil) //nolint:errcheck
				if err := req.Write(stream); err != nil {
					return
				}
				resp, err := http.ReadResponse(bufio.NewReader(stream), req)
				if err != nil {
					return
				}
				body, _ := io.ReadAll(resp.Body) //nolint:errcheck
				resp.Body.Close()                //nolint:errcheck,gosec
				var health map[string]interface{}
				if json.Unmarshal(body, &health) == nil {
					if bi, ok := health["build_info"].(map[string]interface{}); ok {
						if ver, ok := bi["version"].(string); ok {
							out[idx].Version = ver
						}
					}
				}
			}(i, s.PK)
		}
	}
	wg.Wait()
	return out
}

// FetchServiceData fetches data from a deployment service endpoint via the visor's
// configured URLs. service is one of: tpd, ut, sd, ar, rf, dmsgd.
// path is the URL path (e.g., "/all-transports/stats").
func (v *Visor) FetchServiceData(service, path string) ([]byte, error) {
	baseURL := ""
	switch service {
	case "tpd":
		baseURL = v.conf.Transport.Discovery
	case "ut":
		if v.conf.UptimeTracker != nil {
			baseURL = v.conf.UptimeTracker.Addr
		}
	case "sd":
		baseURL = v.conf.Launcher.ServiceDisc
	case "ar":
		baseURL = v.conf.Transport.AddressResolver
	case "rf":
		baseURL = v.conf.Routing.RouteFinder
	case "dmsgd":
		baseURL = v.conf.Dmsg.Discovery
	default:
		return nil, fmt.Errorf("unknown service: %s (valid: tpd, ut, sd, ar, rf, dmsgd)", service)
	}
	if baseURL == "" {
		return nil, fmt.Errorf("service %s not configured", service)
	}

	url := strings.TrimSuffix(baseURL, "/") + path
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Get(url) //nolint:gosec
	if err != nil {
		return nil, fmt.Errorf("fetch %s: %w", url, err)
	}
	defer resp.Body.Close() //nolint:errcheck,gosec

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("service returned %d: %s", resp.StatusCode, string(body))
	}
	return body, nil
}

// servicesFromHTTP queries service discovery for service entries
// matching the given type, with optional version + country filters.
func (v *Visor) servicesFromHTTP(serviceType, version, country string) ([]servicedisc.Service, error) {
	log := logging.MustGetLogger("servicedisc")
	vLog := logging.NewMasterLogger()
	vLog.SetLevel(logrus.InfoLevel)
	sdClient := servicedisc.NewClient(log, vLog, servicedisc.Config{
		Type:          serviceType,
		PK:            v.conf.PK,
		SK:            v.conf.SK,
		DiscAddr:      v.conf.Launcher.ServiceDisc,
		DisplayNodeIP: v.conf.Launcher.DisplayNodeIP,
	}, &http.Client{Timeout: 20 * time.Second}, "")
	return sdClient.Services(context.Background(), 0, version, country)
}

// servicesFromCXO walks the SD services snapshot under
// services/<serviceType>/<pk>/entry and rebuilds a []Service
// honoring optional version + country filters. Returns ok=false when
// the manager isn't installed, the snapshot is empty, or every leaf
// failed to parse — caller falls through to servicesFromHTTP.
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
	return v.servicesFromHTTP(servicedisc.ServiceTypeVPN, version, country)
}

// ProxyServers gets available proxy servers — CXO snapshot first,
// HTTP service-discovery fallback.
func (v *Visor) ProxyServers(version, country string) ([]servicedisc.Service, error) {
	if services, ok := v.servicesFromCXO(servicedisc.ServiceTypeProxy, version, country); ok {
		return services, nil
	}
	return v.servicesFromHTTP(servicedisc.ServiceTypeProxy, version, country)
}

// PublicVisors gets available public visors — CXO snapshot first,
// HTTP service-discovery fallback.
func (v *Visor) PublicVisors(version, country string) ([]servicedisc.Service, error) {
	if services, ok := v.servicesFromCXO(servicedisc.ServiceTypeVisor, version, country); ok {
		return services, nil
	}
	return v.servicesFromHTTP(servicedisc.ServiceTypeVisor, version, country)
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
func (v *Visor) DmsgPtyExec(args DmsgPtyExecArgs) (*dmsgpty.CommandExecResult, error) {
	if v.dmsgPty == nil {
		return nil, fmt.Errorf("dmsgpty: not initialized on this visor")
	}
	if args.RemotePK.Null() {
		return nil, fmt.Errorf("dmsgpty: remote_pk required")
	}
	req := args.Req
	return v.dmsgPty.ExecRemote(context.Background(), args.RemotePK, args.RemotePort, &req)
}

// Ports return list of all ports used by visor services and apps
func (v *Visor) Ports() (map[string]PortDetail, error) {
	ctx := context.Background()
	var ports = make(map[string]PortDetail)

	if v.conf.Hypervisor != nil {
		ports["hypervisor"] = PortDetail{Port: fmt.Sprint(strings.Split(v.conf.Hypervisor.HTTPAddr, ":")[1]), Type: "TCP"}
	}

	ports["dmsg_pty"] = PortDetail{Port: fmt.Sprint(v.conf.Dmsgpty.DmsgPort), Type: "DMSG"}
	ports["cli_addr"] = PortDetail{Port: fmt.Sprint(strings.Split(v.conf.CLIAddr, ":")[1]), Type: "TCP"}
	ports["proc_addr"] = PortDetail{Port: fmt.Sprint(strings.Split(v.conf.Launcher.ServerAddr, ":")[1]), Type: "TCP"}
	ports["stcp_addr"] = PortDetail{Port: fmt.Sprint(strings.Split(v.conf.STCP.ListeningAddress, ":")[1]), Type: "TCP"}

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

// GetRuntimeConfig returns the visor's running config as JSON bytes.
// The SK is included — callers should consider access control.
func (v *Visor) GetRuntimeConfig() ([]byte, error) {
	return json.MarshalIndent(v.conf, "", "  ")
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
