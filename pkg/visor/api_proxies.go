// Package visor pkg/visor/api_proxies.go
//
// Runtime status + live enable/disable for the in-process resolving
// proxies (dmsgweb, skynetweb). Hypervisor UI reads EmbeddedProxies
// to render its "browser proxy" widget and calls SetEmbeddedProxyEnabled
// to toggle resolvers without a visor restart.
//
// Design note: toggling only affects the live runtime. The visor
// config file is NOT rewritten — a restart reverts to whatever
// Enable says on disk. That keeps runtime toggling a "session-scoped
// experiment" rather than a destructive operation, and avoids
// config-file-locking coordination with the config refresh path.
package visor

import (
	"fmt"

	"github.com/skycoin/skywire/pkg/dmsgweb"
	"github.com/skycoin/skywire/pkg/skynetweb"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/logging"
	"github.com/skycoin/skywire/pkg/visor/visorconfig"
)

// EmbeddedProxies reports runtime state for every in-process
// resolver. Both fields are populated when configured — UI layers
// want to show "disabled" as distinct from "absent" so operators
// can tell the difference between "resolver present but toggled
// off" and "config section missing".
func (v *Visor) EmbeddedProxies() (*EmbeddedProxiesStatus, error) {
	out := &EmbeddedProxiesStatus{}
	if v == nil || v.conf == nil {
		return out, nil
	}

	v.initLock.RLock()
	dmsgRuntime := v.embeddedDmsgWeb
	skynetRuntime := v.embeddedSkynetWeb
	v.initLock.RUnlock()

	if cfg := v.conf.DmsgWeb; cfg != nil {
		info := &EmbeddedProxyInfo{
			Enabled:       cfg.Enable,
			DomainSuffix:  stringOrDefault(cfg.DomainSuffix, dmsgweb.DefaultDomainSuffix),
			SocksAddr:     localSocksAddr(cfg.Enable, uintOrDefault(cfg.ProxyPort, defaultDmsgWebProxyPort)),
			WebAddr:       localWebAddr(cfg.Enable, uintOrDefault(cfg.WebPort, defaultDmsgWebPort)),
			UpstreamSOCKS: cfg.UpstreamSOCKS,
		}
		if dmsgRuntime != nil {
			info.Running = dmsgRuntime.IsRunning()
			stats := dmsgRuntime.Stats()
			info.Stats = dmsgStatsToAPI(stats)
		}
		out.DmsgWeb = info
	}
	if cfg := v.conf.SkynetWeb; cfg != nil {
		info := &EmbeddedProxyInfo{
			Enabled:       cfg.Enable,
			DomainSuffix:  stringOrDefault(cfg.DomainSuffix, skynetweb.DefaultDomainSuffix),
			SocksAddr:     localSocksAddr(cfg.Enable, uintOrDefault(cfg.ProxyPort, defaultSkynetWebProxyPort)),
			WebAddr:       localWebAddr(cfg.Enable, uintOrDefault(cfg.WebPort, defaultSkynetWebPort)),
			UpstreamSOCKS: cfg.UpstreamSOCKS,
		}
		if skynetRuntime != nil {
			info.Running = skynetRuntime.IsRunning()
			stats := skynetRuntime.Stats()
			info.Stats = skynetStatsToAPI(stats)
		}
		out.SkynetWeb = info
	}
	return out, nil
}

// SetEmbeddedProxyEnabled starts or stops a resolver at runtime.
// kind is case-insensitive "dmsg" or "skynet". If the resolver was
// never constructed (no config section), it is constructed on-the-fly
// with defaults so `proxies set dmsg on` works from a fresh visor
// without editing the config file.
func (v *Visor) SetEmbeddedProxyEnabled(kind string, enable bool) error {
	if v == nil {
		return fmt.Errorf("visor not initialized")
	}
	switch kind {
	case "dmsg", "dmsgweb", "dmsg_web":
		v.initLock.Lock()
		runtime := v.embeddedDmsgWeb
		if runtime == nil && enable {
			// Construct with defaults — same as if config had
			// {"dmsg_web": {"enable": true}} but without requiring
			// the user to edit the JSON.
			if v.dmsgC == nil {
				v.initLock.Unlock()
				return fmt.Errorf("dmsg client not available; cannot start dmsgweb resolver")
			}
			cfg := &visorconfig.DmsgWebConfig{Enable: true}
			log := logging.MustGetLogger("embedded_dmsgweb")
			runtime = newEmbeddedDmsgWeb(v.ctx, v.dmsgC, cfg, log)
			v.embeddedDmsgWeb = runtime
		}
		v.initLock.Unlock()
		if runtime == nil {
			if !enable {
				return nil // already stopped / never started — no-op
			}
			return fmt.Errorf("embedded dmsgweb could not be constructed")
		}
		if enable {
			return runtime.Start()
		}
		return runtime.Stop()
	case "skynet", "skynetweb", "skynet_web":
		v.initLock.Lock()
		runtime := v.embeddedSkynetWeb
		if runtime == nil && enable {
			if v.router == nil {
				v.initLock.Unlock()
				return fmt.Errorf("router not available; cannot start skynetweb resolver")
			}
			cfg := &visorconfig.SkynetWebConfig{Enable: true}
			log := logging.MustGetLogger("embedded_skynetweb")
			runtime = newEmbeddedSkynetWeb(v.ctx, v.router, cfg, log)
			v.embeddedSkynetWeb = runtime
		}
		v.initLock.Unlock()
		if runtime == nil {
			if !enable {
				return nil
			}
			return fmt.Errorf("embedded skynetweb could not be constructed")
		}
		if enable {
			return runtime.Start()
		}
		return runtime.Stop()
	default:
		return fmt.Errorf("unknown proxy kind %q (want \"dmsg\" or \"skynet\")", kind)
	}
}

// localSocksAddr / localWebAddr return the fully-qualified localhost
// listener or empty when disabled. The render layer uses empty as a
// signal to display "—".
//
// Why condition on `enabled` even when ports are set: a user who
// disabled a resolver via RPC but left the config untouched shouldn't
// see its port advertised. Enabled reflects the current config, not
// the live Running state, so it's the right signal here.
func localSocksAddr(enabled bool, port uint) string {
	if !enabled || port == 0 {
		return ""
	}
	return fmt.Sprintf("127.0.0.1:%d", port)
}

func localWebAddr(enabled bool, port uint) string {
	if !enabled || port == 0 {
		return ""
	}
	return fmt.Sprintf("127.0.0.1:%d", port)
}

// dmsgStatsToAPI / skynetStatsToAPI copy the internal stats snapshot
// to the RPC-surface type. Duplicated rather than factored because
// the source types differ per package (no shared interface) and the
// conversion is trivial; a future type unification would collapse
// both into one adapter.
func dmsgStatsToAPI(s dmsgweb.StatsSnapshot) *EmbeddedProxyStats {
	return &EmbeddedProxyStats{
		StartedAt:     s.StartedAt,
		UptimeSec:     s.UptimeSec,
		TotalRequests: s.TotalRequests,
		Successful:    s.Successful,
		Failed:        s.Failed,
		Active:        s.Active,
		LastRequestAt: s.LastRequestAt,
		LastSuccessAt: s.LastSuccessAt,
		LastFailureAt: s.LastFailureAt,
		LastError:     s.LastError,
	}
}

func skynetStatsToAPI(s skynetweb.StatsSnapshot) *EmbeddedProxyStats {
	return &EmbeddedProxyStats{
		StartedAt:     s.StartedAt,
		UptimeSec:     s.UptimeSec,
		TotalRequests: s.TotalRequests,
		Successful:    s.Successful,
		Failed:        s.Failed,
		Active:        s.Active,
		LastRequestAt: s.LastRequestAt,
		LastSuccessAt: s.LastSuccessAt,
		LastFailureAt: s.LastFailureAt,
		LastError:     s.LastError,
	}
}
