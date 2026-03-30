// api_visor.go contains visor lifecycle and status API methods.
package visor

import (
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/ccding/go-stun/stun"

	"github.com/skycoin/skywire/pkg/app/appserver"
	"github.com/skycoin/skywire/pkg/skyenv"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/buildinfo"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/netutil"
	"github.com/skycoin/skywire/pkg/transport"
	"github.com/skycoin/skywire/pkg/visor/dmsgtracker"
)

// Overview implements API.
func (v *Visor) Overview() (*Overview, error) {
	var tSummaries []*TransportSummary
	var publicIP string
	var isSymmetricNAT bool
	if v == nil {
		return &Overview{}, ErrVisorNotAvailable
	}

	// Collect transport summaries if transport manager and router are ready
	if v.tpM != nil && v.router != nil {
		v.tpM.WalkTransports(func(tp *transport.ManagedTransport) bool {
			tSummaries = append(tSummaries,
				newTransportSummary(v.tpM, tp, true, v.router.SetupIsTrusted(tp.Remote())))
			return true
		})
	}

	if v.isStunReady() {
		switch v.stunClient.NATType {
		case stun.NATNone, stun.NATFull, stun.NATRestricted, stun.NATPortRestricted:
			publicIP = v.stunClient.PublicIP.IP()
			isSymmetricNAT = false
		case stun.NATSymmetric, stun.NATSymmetricUDPFirewall:
			isSymmetricNAT = true
			// Try to get IP from GeoIP service when STUN doesn't provide it
			if ip, err := GetIP(v.conf.GeoIP); err == nil && ip != "" {
				publicIP = ip
			}
		case stun.NATError, stun.NATUnknown, stun.NATBlocked:
			isSymmetricNAT = false
			// STUN failed, try GeoIP service as fallback
			if ip, err := GetIP(v.conf.GeoIP); err == nil && ip != "" {
				publicIP = ip
			} else {
				publicIP = v.stunClient.NATType.String()
			}
		}
	}

	apps := []*appserver.AppState{}
	if v.appL != nil {
		apps = v.appL.AppStates()
	}

	var routesCount int
	if v.router != nil {
		routesCount = v.router.RoutesCount()
	}

	overview := &Overview{
		PubKey:          v.conf.PK,
		BuildInfo:       buildinfo.Get(),
		AppProtoVersion: supportedProtocolVersion,
		Apps:            apps,
		Transports:      tSummaries,
		RoutesCount:     routesCount,
		PublicIP:        publicIP,
		IsSymmetricNAT:  isSymmetricNAT,
	}

	// Add geolocation data if available
	v.geo.mu.RLock()
	if v.geo.data != nil {
		overview.CountryCode = v.geo.data.CountryCode
		overview.RegionName = v.geo.data.RegionName
		overview.CityName = v.geo.data.CityName
		overview.Latitude = v.geo.data.Latitude
		overview.Longitude = v.geo.data.Longitude
	}
	v.geo.mu.RUnlock()

	localIPs, err := netutil.DefaultNetworkInterfaceIPs()
	if err != nil {
		return nil, err
	}

	if len(localIPs) > 0 {
		// should be okay to have the first one, in the case of
		// active network interface, there's usually just a single IP
		overview.LocalIP = localIPs[0].String()
	}

	overview.Hypervisors = v.conf.Hypervisors

	for connectedHV := range v.connectedHypervisors {
		overview.ConnectedHypervisor = append(overview.ConnectedHypervisor, connectedHV)
	}

	return overview, nil
}

// Summary implements API.
func (v *Visor) Summary() (*Summary, error) {
	overview, err := v.Overview()
	if err != nil {
		return nil, fmt.Errorf("failed to get visor overview: %w", err)
	}

	health, err := v.Health()
	if err != nil {
		return nil, fmt.Errorf("failed to get health info: %w", err)
	}

	uptime, err := v.Uptime()
	if err != nil {
		return nil, fmt.Errorf("failed to get uptime: %w", err)
	}

	routes, err := v.RoutingRules()
	if err != nil {
		return nil, fmt.Errorf("failed to get routing rules: %w", err)
	}

	extraRoutes := make([]routingRuleResp, 0, len(routes))
	for _, route := range routes {
		extraRoutes = append(extraRoutes, routingRuleResp{
			Key:     route.KeyRouteID(),
			Rule:    hex.EncodeToString(route),
			Summary: route.Summary(),
		})
	}

	pts, err := v.conf.GetPersistentTransports()
	if err != nil {
		return nil, fmt.Errorf("failed to get persistent transports: %w", err)
	}

	dmsgStatValue := &dmsgtracker.DmsgClientSummary{}
	if v.isDTMReady() {
		if dmsgTracker, ok := v.dtm.Get(v.conf.PK); ok {
			dmsgStatValue = &dmsgTracker
		}
	}

	// Get all connected DMSG servers (deprecated, for backward compatibility)
	var connectedDmsgServers []string
	if v.dmsgC != nil {
		connectedDmsgServers = v.dmsgC.ConnectedServersPK()
	}

	// Get DMSG servers with latency info
	dmsgServers, err := v.DMSGServers()
	if err != nil {
		v.log.WithError(err).Warn("Failed to get DMSG servers info")
		dmsgServers = nil
	}

	rewardAddress, err := v.GetRewardAddress()
	if err != nil {
		v.log.Warn(err)
	}

	summary := &Summary{
		Overview:             overview,
		Health:               health,
		Uptime:               uptime,
		Routes:               extraRoutes,
		MinHops:              v.conf.Routing.MinHops,
		PersistentTransports: pts,
		BuildTag:             runtime.GOOS + "_" + runtime.GOARCH,
		ConfigVersion:        v.conf.Common.Version,
		RewardAddress:        rewardAddress,
		PublicAutoconnect:    v.conf.Transport.PublicAutoconnect,
		DmsgStats:            dmsgStatValue,
		ConnectedDmsgServers: connectedDmsgServers,
		DMSGServers:          dmsgServers,
	}

	return summary, nil
}

// Health implements API.
func (v *Visor) Health() (*HealthInfo, error) {
	if v.isServicesHealthy == nil {
		return &HealthInfo{}, nil
	}
	return &HealthInfo{ServicesHealth: v.isServicesHealthy.value()}, nil
}

// Uptime implements API.
func (v *Visor) Uptime() (float64, error) {
	return time.Since(v.startedAt).Seconds(), nil
}

// RuntimeStats implements API.
func (v *Visor) RuntimeStats() (*RuntimeStatsInfo, error) {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)

	return &RuntimeStatsInfo{
		NumGoroutine:  runtime.NumGoroutine(),
		NumCPU:        runtime.NumCPU(),
		GOMAXPROCS:    runtime.GOMAXPROCS(0),
		GoVersion:     runtime.Version(),
		MemAlloc:      m.Alloc,
		MemTotalAlloc: m.TotalAlloc,
		MemSys:        m.Sys,
		MemHeapAlloc:  m.HeapAlloc,
		MemHeapSys:    m.HeapSys,
		NumGC:         m.NumGC,
	}, nil
}

// SetRewardAddress implements API.
func (v *Visor) SetRewardAddress(p string) (string, error) {
	path := v.conf.LocalPath + "/" + skyenv.RewardFile
	err := os.WriteFile(path, []byte(p), 0600)
	if err != nil {
		return p, fmt.Errorf("failed to write config to file. err=%v", err)
	}
	// generate survey after set/update reward address
	GenerateSurvey(v, v.log, false)
	return p, nil
}

// GetRewardAddress implements API.
func (v *Visor) GetRewardAddress() (string, error) {
	path := v.conf.LocalPath + "/" + skyenv.RewardFile
	_, err := os.Stat(path)
	if os.IsNotExist(err) {
		file, err := os.Create(filepath.Clean(path))
		if err != nil {
			return "", fmt.Errorf("failed to create config file. err=%v", err)
		}
		err = file.Close()
		if err != nil {
			return "", fmt.Errorf("failed to close config file. err=%v", err)
		}
	}
	rConfig, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		return "", fmt.Errorf("failed to read config file. err=%v", err)
	}
	return string(rConfig), nil
}

// DeleteRewardAddress implements API.
func (v *Visor) DeleteRewardAddress() error {

	path := v.conf.LocalPath + "/" + skyenv.RewardFile
	err := os.Remove(path)
	if err != nil {
		return fmt.Errorf("error deleting file. err=%v", err)
	}
	return nil
}

// SkybianBuildVersion implements API.
func (v *Visor) SkybianBuildVersion() string {
	return os.Getenv("SKYBIAN_BUILD_VERSION")
}

// Reload implements API.
func (v *Visor) Reload() error {
	return reload(v)
}

// Shutdown implements API.
func (v *Visor) Shutdown() error {
	defer os.Exit(0)
	return v.Close()
}

// RuntimeLogs returns visor runtime logs
func (v *Visor) RuntimeLogs() (string, error) {
	var builder strings.Builder
	builder.WriteString("[")
	logs, _ := v.logstore.GetLogs()
	builder.WriteString(strings.Join(logs, ","))
	builder.WriteString("]")
	return builder.String(), nil
}
