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

	// Report from config when available, but ALSO from the runtime
	// when the resolver was constructed on-the-fly via RPC (no config
	// section). This ensures `proxies set dmsg on` → `proxies` shows
	// the running state even without a config entry.
	if cfg := v.conf.DmsgWeb; cfg != nil {
		out.DmsgWeb = dmsgProxyInfo(cfg, dmsgRuntime)
	} else if dmsgRuntime != nil {
		// Runtime exists but config is nil — constructed on-the-fly.
		out.DmsgWeb = dmsgProxyInfo(&visorconfig.DmsgWebConfig{Enable: true}, dmsgRuntime)
	}
	if cfg := v.conf.SkynetWeb; cfg != nil {
		out.SkynetWeb = skynetProxyInfo(cfg, skynetRuntime)
	} else if skynetRuntime != nil {
		out.SkynetWeb = skynetProxyInfo(&visorconfig.SkynetWebConfig{Enable: true}, skynetRuntime)
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
			if v.dmsgC == nil {
				v.initLock.Unlock()
				return fmt.Errorf("dmsg client not available; cannot start dmsgweb resolver")
			}
			cfg := &visorconfig.DmsgWebConfig{Enable: true}
			// Auto-chain: set dmsgweb's upstream to skynetweb's
			// SOCKS5 port so the browser only needs one proxy
			// entry (localhost:4445) for both .dmsg and .skynet.
			// If skynetweb isn't running yet, the upstream
			// connection simply refuses non-.dmsg requests until
			// it starts — no data loss, just "not available yet".
			cfg.UpstreamSOCKS = fmt.Sprintf("127.0.0.1:%d", defaultSkynetWebProxyPort)
			log := logging.MustGetLogger("embedded_dmsgweb")
			runtime = newEmbeddedDmsgWeb(v.ctx, v.dmsgC, cfg, log)
			v.embeddedDmsgWeb = runtime
		}
		v.initLock.Unlock()
		if runtime == nil {
			if !enable {
				return nil
			}
			return fmt.Errorf("embedded dmsgweb could not be constructed")
		}
		if enable {
			if err := runtime.Start(); err != nil {
				return err
			}
			// If skynetweb isn't running yet, start it too so the
			// auto-chain works immediately. Ignore errors — skynet
			// is optional.
			v.autoStartSkynetWeb()
			return nil
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
			runtime = newEmbeddedSkynetWeb(v.ctx, v.router, v.conf.PK, cfg, log)
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

// SetEmbeddedProxyUpstream changes the upstream SOCKS5 address for a
// resolving proxy at runtime. The resolver is restarted to apply the
// change. Pass "" to clear the upstream.
func (v *Visor) SetEmbeddedProxyUpstream(kind, addr string) error {
	switch kind {
	case "dmsg", "dmsgweb", "dmsg_web":
		v.initLock.Lock()
		runtime := v.embeddedDmsgWeb
		v.initLock.Unlock()
		if runtime == nil {
			return fmt.Errorf("dmsgweb resolver not initialized")
		}
		return runtime.SetUpstream(addr)
	case "skynet", "skynetweb", "skynet_web":
		v.initLock.Lock()
		runtime := v.embeddedSkynetWeb
		v.initLock.Unlock()
		if runtime == nil {
			return fmt.Errorf("skynetweb resolver not initialized")
		}
		return runtime.SetUpstream(addr)
	default:
		return fmt.Errorf("unknown proxy kind %q (want \"dmsg\" or \"skynet\")", kind)
	}
}

func dmsgProxyInfo(cfg *visorconfig.DmsgWebConfig, runtime *EmbeddedDmsgWeb) *EmbeddedProxyInfo {
	info := &EmbeddedProxyInfo{
		Enabled:       cfg.Enable,
		DomainSuffix:  stringOrDefault(cfg.DomainSuffix, dmsgweb.DefaultDomainSuffix),
		SocksAddr:     localSocksAddr(true, uintOrDefault(cfg.ProxyPort, defaultDmsgWebProxyPort)),
		WebAddr:       localWebAddr(true, uintOrDefault(cfg.WebPort, defaultDmsgWebPort)),
		UpstreamSOCKS: cfg.UpstreamSOCKS,
	}
	if runtime != nil {
		info.Running = runtime.IsRunning()
		info.Stats = dmsgStatsToAPI(runtime.Stats())
		if u := runtime.Upstream(); u != "" {
			info.UpstreamSOCKS = u
		}
	}
	return info
}

func skynetProxyInfo(cfg *visorconfig.SkynetWebConfig, runtime *EmbeddedSkynetWeb) *EmbeddedProxyInfo {
	info := &EmbeddedProxyInfo{
		Enabled:       cfg.Enable,
		DomainSuffix:  stringOrDefault(cfg.DomainSuffix, skynetweb.DefaultDomainSuffix),
		SocksAddr:     localSocksAddr(true, uintOrDefault(cfg.ProxyPort, defaultSkynetWebProxyPort)),
		WebAddr:       localWebAddr(true, uintOrDefault(cfg.WebPort, defaultSkynetWebPort)),
		UpstreamSOCKS: cfg.UpstreamSOCKS,
	}
	if runtime != nil {
		info.Running = runtime.IsRunning()
		info.Stats = skynetStatsToAPI(runtime.Stats())
		if u := runtime.Upstream(); u != "" {
			info.UpstreamSOCKS = u
		}
	}
	return info
}

// autoStartSkynetWeb is a best-effort helper called after dmsgweb
// starts. It ensures skynetweb is also running so the auto-chain
// (dmsgweb upstream → skynetweb) works out of the box. If skynetweb
// can't be constructed (no router, etc.) the error is silently
// swallowed — .dmsg still works, just .skynet won't resolve until
// the router is available and `proxies set skynet on` is called.
func (v *Visor) autoStartSkynetWeb() {
	v.initLock.Lock()
	runtime := v.embeddedSkynetWeb
	if runtime == nil {
		if v.router == nil {
			v.initLock.Unlock()
			return
		}
		cfg := &visorconfig.SkynetWebConfig{Enable: true}
		log := logging.MustGetLogger("embedded_skynetweb")
		runtime = newEmbeddedSkynetWeb(v.ctx, v.router, v.conf.PK, cfg, log)
		v.embeddedSkynetWeb = runtime
	}
	v.initLock.Unlock()
	if !runtime.IsRunning() {
		_ = runtime.Start() //nolint:errcheck
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
