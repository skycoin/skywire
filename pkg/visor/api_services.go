// api_services.go contains service discovery, health, and configuration API methods.
package visor

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/skycoin/skywire/pkg/servicedisc"
	"github.com/skycoin/skywire/pkg/skyenv"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/logging"
	"github.com/skycoin/skywire/pkg/visor/visorconfig"
)

// ServiceHealth checks the health of all configured deployment services.
func (v *Visor) ServiceHealth() ([]ServiceHealthEntry, error) {
	services := map[string]string{
		"Transport Discovery": v.conf.Transport.Discovery,
		"Address Resolver":    v.conf.Transport.AddressResolver,
		"Route Finder":        v.conf.Routing.RouteFinder,
		"DMSG Discovery":      v.conf.Dmsg.Discovery,
	}
	if v.conf.UptimeTracker != nil {
		services["Uptime Tracker"] = v.conf.UptimeTracker.Addr
	}

	client := &http.Client{Timeout: 10 * time.Second}
	var results []ServiceHealthEntry

	for name, baseURL := range services {
		if baseURL == "" {
			continue
		}
		url := strings.TrimSuffix(baseURL, "/") + "/health"
		entry := ServiceHealthEntry{Name: name, URL: baseURL}

		start := time.Now()
		resp, err := client.Get(url) //nolint:gosec
		entry.LatencyMs = time.Since(start).Milliseconds()

		if err != nil {
			entry.Status = "DOWN"
			results = append(results, entry)
			continue
		}

		body, _ := io.ReadAll(resp.Body) //nolint:errcheck,gosec
		resp.Body.Close()                //nolint:errcheck,gosec

		if resp.StatusCode != http.StatusOK {
			entry.Status = fmt.Sprintf("ERROR(%d)", resp.StatusCode)
			results = append(results, entry)
			continue
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
		results = append(results, entry)
	}

	return results, nil
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

// PublicAutoconnectStatus returns whether public autoconnect is currently running
func (v *Visor) PublicAutoconnectStatus() (bool, error) {
	return v.IsPublicAutoconnectRunning(), nil
}
