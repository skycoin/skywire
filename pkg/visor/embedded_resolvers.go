// Package visor pkg/visor/embedded_resolvers.go c3-vis-core
//
// initEmbeddedResolvers brings up the ADDITIONAL resolving proxies declared by
// the `resolvers` config list — the ones beyond the dmsg_web / skynet_web
// primaries that embedded_dmsgweb.go and embedded_skynetweb.go own.
//
// There is no new runtime here. Each entry is projected onto the same
// DmsgWebConfig / SkynetWebConfig the primaries use and handed to the same
// EmbeddedDmsgWeb / EmbeddedSkynetWeb type, so an extra resolver is not a
// second implementation that can drift from the first — it is another instance
// of the one that already works, with a different listener and, for a dmsg
// resolver, optionally a different identity.
//
// What IS new is the failure this file has to avoid. The primaries could not
// collide with each other (4445 vs 4446, fixed), so nothing ever checked. With
// N resolvers the operator picks the ports, and two SOCKS5 listeners on one
// port do not both fail loudly: the first binds, the second's Run loop returns
// "address already in use" into a log line, and the app row still says running.
// So the port map is validated before anything is constructed, and a resolver
// that loses a conflict is not constructed at all — with an ERROR naming both
// claimants.
package visor

import (
	"context"
	"fmt"

	"github.com/skycoin/skywire/pkg/app/appcommon"
	"github.com/skycoin/skywire/pkg/app/launcher"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/skyenv"
	"github.com/skycoin/skywire/pkg/visor/visorconfig"
)

// embeddedResolver pairs one `resolvers` entry with the runtime it drives.
// Exactly one of dmsg / skynet is non-nil, selected by the entry's kind.
type embeddedResolver struct {
	// name is the entry's resolver name; appName is the launcher row
	// ("resolver-<name>").
	name    string
	appName string
	// enable mirrors the config at construction, so initLauncher can decide
	// autostart without re-deriving it from the (possibly reordered) list.
	enable bool
	dmsg   *EmbeddedDmsgWeb
	skynet *EmbeddedSkynetWeb
}

// initEmbeddedResolvers constructs every additional resolver and registers it
// as a launcher app named "resolver-<name>". Construction happens whether or
// not the entry is enabled — same reasoning as the primaries: the launcher can
// then start a disabled resolver on operator command without a visor restart.
//
// Runs after the router module so a skynet entry has v.router; a dmsg entry's
// dependency (v.dmsgC) is satisfied transitively by the same module.
func initEmbeddedResolvers(ctx context.Context, v *Visor, log *logging.Logger) error {
	if v.conf == nil || len(v.conf.Resolvers) == 0 {
		return nil
	}

	// One check for the whole set, including the primaries: a conflict between
	// an extra resolver and dmsg_web is exactly as silent as one between two
	// extras. Refusing to construct ANY of them on a conflict is deliberate —
	// picking a winner would leave the operator with a listener set they did
	// not ask for and no clear signal which entry was dropped.
	if err := v.conf.ValidateResolvers(); err != nil {
		log.WithError(err).Error("resolvers config rejected; no additional resolving proxies will run")
		return nil
	}

	aliases, dmsgSet := resolverAliasesAndDmsgServers(v)
	out := make([]*embeddedResolver, 0, len(v.conf.Resolvers))
	for i := range v.conf.Resolvers {
		rc := &v.conf.Resolvers[i]
		rlog := v.MasterLogger().PackageLogger("resolver_" + rc.Name)
		er := &embeddedResolver{name: rc.Name, appName: rc.AppName(), enable: rc.Enable}

		if rc.IsSkynet() {
			if v.router == nil {
				log.WithField("resolver", rc.Name).Warn("skynet resolver configured but router not available; skipping")
				continue
			}
			cfg := rc.ToSkynetWeb()
			applyResolverChain(v, rc, &cfg.UpstreamSOCKS, rlog)
			rt := newEmbeddedSkynetWeb(ctx, v.router, v.tpM, &v.skynetFwdMux, v.conf.PK,
				v.services.SelfDial, v.services.SelfDialAs, cfg, rlog)
			rt.statusProvider = v.proxyStatusProvider()
			er.skynet = rt
		} else {
			if v.dmsgC == nil {
				log.WithField("resolver", rc.Name).Warn("dmsg resolver configured but dmsg client not available; skipping")
				continue
			}
			cfg := rc.ToDmsgWeb()
			applyResolverChain(v, rc, &cfg.UpstreamSOCKS, rlog)
			// Own identity when secret_key is set, the visor's otherwise. A
			// failure here is not fatal for the same reason it isn't for
			// dmsg_web: falling back keeps the resolver working, and the log
			// says the identity is not the configured one — the part the
			// operator must know, since the survey whitelist is keyed on it.
			resolverC, resolverPK := v.dmsgC, v.conf.PK
			if guest, guestPK, err := v.dmsgWebGuestClient(ctx, "resolver_"+rc.Name, cfg, rlog); err != nil {
				log.WithError(err).WithField("resolver", rc.Name).
					Error("resolver secret_key configured but its client could not be attached; " +
						"falling back to the visor's identity")
			} else if guest != nil {
				resolverC, resolverPK = guest, guestPK
			}
			rt := newEmbeddedDmsgWeb(ctx, resolverC, v.dmsgDC, resolverPK,
				v.services.SelfDial, v.services.SelfDialAs, aliases, dmsgSet, cfg, rlog)
			rt.statusProvider = v.proxyStatusProvider()
			er.dmsg = rt
		}

		launcher.RegisterApp(er.appName, buildResolverAppFunc(er, rlog))
		out = append(out, er)
		log.WithField("resolver", er.name).
			WithField("kind", resolverKindLabel(rc)).
			WithField("listen", visorconfig.ResolverListenAddr(rc.ProxyAddr, rc.ProxyPort)).
			WithField("enable", rc.Enable).
			Info("Constructed additional resolving proxy")
	}

	v.initLock.Lock()
	v.embeddedResolvers = out
	v.initLock.Unlock()
	return nil
}

// resolverKindLabel renders an entry's kind for logs, spelling the empty
// (default) kind out as "dmsg" rather than showing a blank field.
func resolverKindLabel(rc *visorconfig.ResolverConfig) string {
	if rc.IsSkynet() {
		return visorconfig.ResolverKindSkynet
	}
	return visorconfig.ResolverKindDmsg
}

// applyResolverChain fills an empty upstream with the same auto-chain the
// matching primary gets, unless the entry opted out with chain:false.
//
// The two chains mirror initEmbeddedDmsgWeb / initEmbeddedSkynetWeb exactly:
// a dmsg resolver points at the skynet_web listener so one browser proxy entry
// covers both TLDs, and a skynet resolver points at skysocks-client so
// clearnet exits over the mesh. Opting out matters for a LAN-facing resolver —
// chained, it would hand every device on the network a working clearnet SOCKS5
// proxy, which is an open relay nobody asked for.
func applyResolverChain(v *Visor, rc *visorconfig.ResolverConfig, upstream *string, log *logging.Logger) {
	if *upstream != "" || !rc.Chained() {
		return
	}
	if rc.IsSkynet() {
		if v.conf.Launcher == nil {
			return
		}
		for _, ac := range v.conf.Launcher.Apps {
			if ac.Name == skyenv.SkysocksClientName && ac.AutoStart {
				*upstream = skyenv.SkysocksClientAddr
				log.WithField("upstream", *upstream).Info("Auto-chaining resolver → skysocks-client for clearnet")
				return
			}
		}
		return
	}
	if v.conf.SkynetWeb == nil {
		return
	}
	// Never chain a resolver to its own listener: with suffix/port picked by
	// hand an extra .dmsg resolver CAN be configured on skynet_web's port, and
	// a self-chain is an instant loop that eats the connection instead of
	// failing.
	port := v.conf.SkynetWeb.ProxyPort
	if port == 0 {
		port = defaultSkynetWebProxyPort
	}
	if port == rc.ProxyPort {
		return
	}
	*upstream = fmt.Sprintf("127.0.0.1:%d", port)
	log.WithField("upstream", *upstream).Info("Auto-chaining resolver → skynetweb for unified proxy")
}

// buildResolverAppFunc wraps an additional resolver in the launcher's AppFunc
// contract. Same shape as buildDmsgWebAppFunc; see it for why the app client
// handshake is opened here rather than in Start.
func buildResolverAppFunc(er *embeddedResolver, log *logging.Logger) appcommon.AppFunc {
	if er.dmsg != nil {
		return buildDmsgWebAppFunc(er.dmsg, log)
	}
	return buildSkynetWebAppFunc(er.skynet, log)
}
