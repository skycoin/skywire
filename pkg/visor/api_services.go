// api_services.go contains service discovery, health, and configuration API methods.
package visor

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/skycoin/skywire/deployment"
	dmsgdisc "github.com/skycoin/skywire/pkg/dmsg/disc"
	"github.com/skycoin/skywire/pkg/dmsg/dmsghttp"
	"github.com/skycoin/skywire/pkg/servicedisc"
	"github.com/skycoin/skywire/pkg/skyenv"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/logging"
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
	type svcDef struct {
		name, url string
	}
	// Prefer dmsg URL when configured; fall back to HTTP.
	preferDmsg := func(httpURL, dmsgURL string) string {
		if dmsgURL != "" {
			return dmsgURL
		}
		return httpURL
	}
	httpServices := []svcDef{
		{"Config Service", deployment.ProdConf.Conf},
		{"Transport Discovery", preferDmsg(v.conf.Transport.Discovery, v.conf.Transport.DiscoveryDmsg)},
		{"DMSG Discovery", preferDmsg(v.conf.Dmsg.Discovery, v.conf.Dmsg.DiscoveryDmsg)},
		{"Address Resolver", preferDmsg(v.conf.Transport.AddressResolver, v.conf.Transport.AddressResolverDmsg)},
		{"Route Finder", preferDmsg(v.conf.Routing.RouteFinder, v.conf.Routing.RouteFinderDmsg)},
		{"Service Discovery", preferDmsg(v.conf.Launcher.ServiceDisc, v.conf.Launcher.ServiceDiscDmsg)},
	}
	if v.conf.UptimeTracker != nil {
		httpServices = append(httpServices, svcDef{"Uptime Tracker", preferDmsg(v.conf.UptimeTracker.Addr, v.conf.UptimeTracker.AddrDmsg)})
	}

	// One client for plain http/https, one for dmsg:// URLs. The dmsg
	// client is only built if the visor has an active DMSG client.
	httpClient := &http.Client{Timeout: 10 * time.Second}
	var dmsgClient *http.Client
	if v.dmsgC != nil {
		dmsgClient = &http.Client{
			Transport: dmsghttp.MakeHTTPTransport(context.Background(), v.dmsgC),
			Timeout:   10 * time.Second,
		}
	}

	httpResults := make([]ServiceHealthEntry, len(httpServices))
	var wg sync.WaitGroup
	for i, svc := range httpServices {
		if svc.url == "" {
			httpResults[i] = ServiceHealthEntry{Name: svc.name, Status: "N/A"}
			continue
		}
		wg.Add(1)
		go func(i int, name, baseURL string) {
			defer wg.Done()
			httpResults[i] = probeServiceHealth(httpClient, dmsgClient, name, baseURL)
		}(i, svc.name, svc.url)
	}
	wg.Wait()

	results := make([]ServiceHealthEntry, 0, len(httpResults)+16)
	for _, e := range httpResults {
		if e.Name != "" {
			results = append(results, e)
		}
	}

	// ---------- DMSG servers (currently connected) ----------
	if v.dmsgC != nil {
		results = append(results, v.dmsgServerHealth()...)
	}

	// ---------- Route Setup Nodes & Transport Setup Nodes ----------
	rsnPKs := v.conf.EffectiveRouteSetupNodes()
	tpsPKs := v.conf.EffectiveTransportSetupPKs()
	if len(rsnPKs) > 0 || len(tpsPKs) > 0 {
		// The dmsg discovery client needs a plain http.Client; it will
		// use whichever scheme the DmsgDiscovery URL carries. If the
		// discovery is itself dmsg://, we need the dmsg transport.
		discHTTPC := httpClient
		if dmsgClient != nil && strings.HasPrefix(v.conf.Dmsg.Discovery, "dmsg://") {
			discHTTPC = dmsgClient
		}
		discClient := dmsgdisc.NewHTTP(v.conf.Dmsg.Discovery, discHTTPC, v.log)
		results = append(results, dmsgDiscoveryHealth(discClient, "Route Setup Node", rsnPKs)...)
		results = append(results, dmsgDiscoveryHealth(discClient, "Transport Setup Node", tpsPKs)...)
	}

	return results, nil
}

// probeServiceHealth performs one GET {baseURL}/health and returns a
// populated ServiceHealthEntry. It picks the transport based on the URL
// scheme: dmsg:// goes through the visor's DMSG client, everything else
// goes through the plain HTTP client. Safe to call concurrently.
func probeServiceHealth(httpClient, dmsgClient *http.Client, name, baseURL string) ServiceHealthEntry {
	entry := ServiceHealthEntry{Name: name, URL: baseURL}

	u, parseErr := url.Parse(baseURL)
	if parseErr != nil {
		entry.Status = "DOWN"
		return entry
	}
	client := httpClient
	if u.Scheme == "dmsg" {
		if dmsgClient == nil {
			entry.Status = "N/A" // visor has no active DMSG client
			return entry
		}
		client = dmsgClient
	}

	reqURL := strings.TrimSuffix(baseURL, "/") + "/health"
	start := time.Now()
	resp, err := client.Get(reqURL) //nolint:gosec
	entry.LatencyMs = time.Since(start).Milliseconds()

	if err != nil {
		entry.Status = "DOWN"
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

// dmsgServerHealth returns one ServiceHealthEntry per DMSG server the
// visor currently holds an active session with. Latency is the last
// measured ping RTT (0 if unmeasured). Entries are sorted by PK so the
// UI order remains stable across polls (DMSGServers() sorts by latency
// which flips between samples and causes visible reordering).
func (v *Visor) dmsgServerHealth() []ServiceHealthEntry {
	servers, err := v.DMSGServers()
	if err != nil || len(servers) == 0 {
		return nil
	}
	sort.Slice(servers, func(i, j int) bool {
		return servers[i].PK.String() < servers[j].PK.String()
	})
	out := make([]ServiceHealthEntry, 0, len(servers))
	for _, s := range servers {
		entry := ServiceHealthEntry{
			Name:      "DMSG Server " + s.PK.String()[:8],
			URL:       "dmsg://" + s.PK.String(),
			Status:    "OK", // active session means it's healthy for us
			LatencyMs: s.Latency.Milliseconds(),
		}
		out = append(out, entry)
	}
	return out
}

// dmsgDiscoveryHealth probes RSN/TPS nodes by querying the DMSG discovery
// for each PK's entry. An entry with a non-empty DelegatedServers list is
// considered reachable; anything else is DOWN. Results are ordered by PK
// so the UI order is stable.
func dmsgDiscoveryHealth(discClient dmsgdisc.APIClient, label string, pks []cipher.PubKey) []ServiceHealthEntry {
	if len(pks) == 0 {
		return nil
	}
	// Sort PKs for stable output ordering.
	sorted := make([]cipher.PubKey, len(pks))
	copy(sorted, pks)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].String() < sorted[j].String() })

	results := make([]ServiceHealthEntry, len(sorted))
	var wg sync.WaitGroup
	for i, pk := range sorted {
		wg.Add(1)
		go func(i int, pk cipher.PubKey) {
			defer wg.Done()
			entry := ServiceHealthEntry{
				Name: label + " " + pk.String()[:8],
				URL:  "dmsg://" + pk.String(),
			}
			ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
			defer cancel()
			start := time.Now()
			dEntry, err := discClient.Entry(ctx, pk)
			entry.LatencyMs = time.Since(start).Milliseconds()
			if err != nil || dEntry == nil || dEntry.Client == nil || len(dEntry.Client.DelegatedServers) == 0 {
				entry.Status = "DOWN"
			} else {
				entry.Status = "OK"
			}
			results[i] = entry
		}(i, pk)
	}
	wg.Wait()
	return results
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

// VPNServers gets available public VPN server from service discovery URL
func (v *Visor) VPNServers(version, country string) ([]servicedisc.Service, error) {
	log := logging.MustGetLogger("vpnservers")
	vLog := logging.NewMasterLogger()
	vLog.SetLevel(logrus.InfoLevel)

	sdClient := servicedisc.NewClient(log, vLog, servicedisc.Config{
		Type:          servicedisc.ServiceTypeVPN,
		PK:            v.conf.PK,
		SK:            v.conf.SK,
		DiscAddr:      v.conf.Launcher.ServiceDisc,
		DisplayNodeIP: v.conf.Launcher.DisplayNodeIP,
	}, &http.Client{Timeout: time.Duration(20) * time.Second}, "")
	vpnServers, err := sdClient.Services(context.Background(), 0, version, country)
	if err != nil {
		v.log.Error("Error getting public vpn servers: ", err)
		return nil, err
	}
	return vpnServers, nil
}

// ProxyServers gets available public VPN server from service discovery URL
func (v *Visor) ProxyServers(version, country string) ([]servicedisc.Service, error) {
	log := logging.MustGetLogger("proxyservers")
	vLog := logging.NewMasterLogger()
	vLog.SetLevel(logrus.InfoLevel)

	sdClient := servicedisc.NewClient(log, vLog, servicedisc.Config{
		Type:          servicedisc.ServiceTypeProxy,
		PK:            v.conf.PK,
		SK:            v.conf.SK,
		DiscAddr:      v.conf.Launcher.ServiceDisc,
		DisplayNodeIP: v.conf.Launcher.DisplayNodeIP,
	}, &http.Client{Timeout: time.Duration(20) * time.Second}, "")
	proxyServers, err := sdClient.Services(context.Background(), 0, version, country)
	if err != nil {
		v.log.Error("Error getting public vpn servers: ", err)
		return nil, err
	}
	return proxyServers, nil
}

// PublicVisors gets available public public visors from service discovery URL
func (v *Visor) PublicVisors(version, country string) ([]servicedisc.Service, error) {
	log := logging.MustGetLogger("public_visors")
	vLog := logging.NewMasterLogger()
	vLog.SetLevel(logrus.InfoLevel)

	sdClient := servicedisc.NewClient(log, vLog, servicedisc.Config{
		Type:          servicedisc.ServiceTypeVisor,
		PK:            v.conf.PK,
		SK:            v.conf.SK,
		DiscAddr:      v.conf.Launcher.ServiceDisc,
		DisplayNodeIP: v.conf.Launcher.DisplayNodeIP,
	}, &http.Client{Timeout: time.Duration(20) * time.Second}, "")
	publicVisors, err := sdClient.Services(context.Background(), 0, version, country)
	if err != nil {
		v.log.Error("Error getting public vpn servers: ", err)
		return nil, err
	}
	return publicVisors, nil
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

// PublicAutoconnectStatus returns whether public autoconnect is currently running
func (v *Visor) PublicAutoconnectStatus() (bool, error) {
	return v.IsPublicAutoconnectRunning(), nil
}
