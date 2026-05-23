// api_visor.go contains visor lifecycle and status API methods.
package visor

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/ccding/go-stun/stun"

	"github.com/skycoin/skywire/pkg/app/appserver"
	"github.com/skycoin/skywire/pkg/buildinfo"
	"github.com/skycoin/skywire/pkg/netutil"
	"github.com/skycoin/skywire/pkg/skyenv"
	"github.com/skycoin/skywire/pkg/transport"
	"github.com/skycoin/skywire/pkg/visor/dmsgtracker"
	"github.com/skycoin/skywire/pkg/visor/visorconfig"
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
		switch v.stun.client.NATType {
		case stun.NATNone, stun.NATFull, stun.NATRestricted, stun.NATPortRestricted:
			publicIP = v.stun.client.PublicIP.IP()
			isSymmetricNAT = false
		case stun.NATSymmetric, stun.NATSymmetricUDPFirewall:
			isSymmetricNAT = true
		case stun.NATError, stun.NATUnknown, stun.NATBlocked:
			isSymmetricNAT = false
			publicIP = v.stun.client.NATType.String()
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

	routeGroups, err := v.RouteGroups()
	if err != nil {
		v.log.WithError(err).Warn("Failed to get route groups for summary")
		routeGroups = nil
	}

	pts, err := v.conf.GetPersistentTransports()
	if err != nil {
		return nil, fmt.Errorf("failed to get persistent transports: %w", err)
	}

	dmsgStatValue := &dmsgtracker.DmsgClientSummary{}
	if v.isDTMReady() {
		if dmsgTracker, ok := v.dmsgTracker.manager.Get(v.conf.PK); ok {
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
		RouteGroups:          routeGroups,
		MinHops:              v.conf.Routing.MinHops,
		PersistentTransports: pts,
		BuildTag:             runtime.GOOS + "_" + runtime.GOARCH,
		ConfigVersion:        v.conf.Common.Version,
		RewardAddress:        rewardAddress,
		PublicAutoconnect:    v.conf.Transport.PublicAutoconnect,
		IsPublic:             v.conf.IsPublic,
		DmsgStats:            dmsgStatValue,
		ConnectedDmsgServers: connectedDmsgServers,
		DMSGServers:          dmsgServers,
		IsHypervisor:         v.IsHypervisorEnabled(),
	}
	if v.conf.Hypervisor != nil {
		summary.HypervisorAddr = v.conf.Hypervisor.HTTPAddr
	}

	return summary, nil
}

// Health implements API.
func (v *Visor) Health() (*HealthInfo, error) {
	if v.isServicesHealthy == nil {
		return &HealthInfo{}, nil
	}
	hi := &HealthInfo{
		ServicesHealth: v.isServicesHealthy.value(),
	}
	if v.isUptimeTrackerHealthy != nil {
		hi.UptimeTrackerHealth = v.isUptimeTrackerHealthy.value()
	}
	if v.isAutoconnectHealthy != nil {
		hi.AutoconnectHealth = v.isAutoconnectHealthy.value()
	}
	if v.isTransportabilityHealthy != nil {
		hi.TransportabilityHealth = v.isTransportabilityHealthy.value()
	}
	return hi, nil
}

// EnableHypervisor implements API.
func (v *Visor) EnableHypervisor() error {
	if v.hvInstance == nil {
		return fmt.Errorf("hypervisor not configured — add \"hypervisor\" section to visor config")
	}
	return v.hvInstance.Enable(context.Background())
}

// DisableHypervisor implements API.
func (v *Visor) DisableHypervisor() error {
	if v.hvInstance == nil {
		return nil
	}
	return v.hvInstance.Disable()
}

// IsHypervisorEnabled implements API.
func (v *Visor) IsHypervisorEnabled() bool {
	if v.hvInstance == nil {
		return false
	}
	return v.hvInstance.IsEnabled()
}

// EnableHypervisorPersist enables the hypervisor and optionally persists to config.
func (v *Visor) EnableHypervisorPersist(persist bool) error {
	if err := v.EnableHypervisor(); err != nil {
		return err
	}
	if persist {
		return v.persistHypervisorEnabled(true)
	}
	return nil
}

// DisableHypervisorPersist disables the hypervisor and optionally persists to config.
func (v *Visor) DisableHypervisorPersist(persist bool) error {
	if err := v.DisableHypervisor(); err != nil {
		return err
	}
	if persist {
		return v.persistHypervisorEnabled(false)
	}
	return nil
}

// persistHypervisorEnabled writes the hypervisor enable state to the config file.
func (v *Visor) persistHypervisorEnabled(enable bool) error {
	if v.conf.Hypervisor == nil {
		config := visorconfig.DefaultHypervisorConfig()
		v.conf.Hypervisor = &config
	}
	v.conf.Hypervisor.Enable = enable
	return v.conf.Flush()
}

// IsStartupComplete implements API.
func (v *Visor) IsStartupComplete() bool {
	select {
	case <-v.startupComplete:
		return true
	default:
		return false
	}
}

// Uptime implements API.
func (v *Visor) Uptime() (float64, error) {
	return time.Since(v.startedAt).Seconds(), nil
}

// UptimeHistory implements API. Reads from the service-self uptime
// recorder's bbolt store. Returns ErrUptimeRecorderUnavailable when
// the recorder isn't wired (recorder open failed at startup or the
// visor is running without LocalPath access).
func (v *Visor) UptimeHistory(args UptimeHistoryArgs) (*UptimeHistoryResponse, error) {
	rec := v.UptimeRecorder()
	if rec == nil {
		return nil, ErrUptimeRecorderUnavailable
	}
	store := rec.Store()
	sessions, err := store.Sessions(args.Since)
	if err != nil {
		return nil, fmt.Errorf("read sessions: %w", err)
	}
	if args.Limit > 0 && args.Limit < len(sessions) {
		sessions = sessions[len(sessions)-args.Limit:]
	}
	resp := &UptimeHistoryResponse{
		Current:  rec.CurrentSession(),
		Sessions: sessions,
	}
	if args.IncludeTimeline {
		date := args.TimelineDate
		if date.IsZero() {
			date = time.Now().UTC()
		}
		bm, err := store.Bitmap(date)
		if err != nil {
			return nil, fmt.Errorf("read timeline: %w", err)
		}
		resp.Timeline = bm
		resp.TimelineDate = date.UTC().Format("2006-01-02")
	}
	return resp, nil
}

// ErrUptimeRecorderUnavailable is returned by UptimeHistory when the
// recorder failed to open or wasn't wired into the visor (e.g. the
// LocalPath is unwritable). Callers should distinguish this from a
// "the recorder ran but has no data yet" empty response.
var ErrUptimeRecorderUnavailable = errors.New("service-self uptime recorder not configured")

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
	// Write to config
	v.conf.RewardAddress = p
	// Also write to reward file for backward compatibility
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
	// Prefer config value
	if v.conf.RewardAddress != "" {
		return v.conf.RewardAddress, nil
	}
	// Fall back to reward file
	path := v.conf.LocalPath + "/" + skyenv.RewardFile
	rConfig, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", fmt.Errorf("failed to read reward file: %v", err)
	}
	return strings.TrimSpace(string(rConfig)), nil
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

// RuntimeLogsSince returns only entries whose log_line is strictly
// greater than since. Used for diff-based live tailing of the
// runtime-logs buffer; pass the previous response's Latest as
// `since` to fetch only the new entries since the last poll.
func (v *Visor) RuntimeLogsSince(since int64) (RuntimeLogsDelta, error) {
	logs, dropped, latest := v.logstore.GetLogsSince(since)
	return RuntimeLogsDelta{
		Entries: logs,
		Latest:  latest,
		Dropped: dropped,
	}, nil
}
