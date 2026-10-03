//go:build !mobile

// Package visor pkg/visor/init_dmsg_server.go c3-vis-core
//
// The in-process dmsg SERVER (Dmsg.Server in the config). Desktop-only: the
// standalone service it can run (pkg/services/dmsgsrv) pulls VictoriaMetrics,
// which the mobile build cannot compile for iOS, and a phone is never a dmsg
// server. The mobile module set registers dmsg_server as a no-op
// (init_modules_mobile.go), so nothing there refers to these functions.
package visor

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/skycoin/skywire/deployment"
	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/cmdutil"
	dmsgdisc "github.com/skycoin/skywire/pkg/dmsg/disc"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
	dmsgmetrics "github.com/skycoin/skywire/pkg/dmsg/dmsg/metrics"
	"github.com/skycoin/skywire/pkg/dmsg/dmsghttp"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/services/dmsgsrv"
	"github.com/skycoin/skywire/pkg/visor/visorapi"
)

// dmsgOnlyDisc returns a disc.APIClient that registers + resolves the
// discovery STRICTLY over dmsg, riding the visor's existing dmsg client —
// no plain-HTTP egress and no second transit client. Replicated from
// pkg/services/dmsgsrv.newDmsgOnly to avoid pulling that heavy service
// package (and its import cycle) into pkg/visor. dmsgC must be able to
// resolve discPK over dmsg.
func dmsgOnlyDisc(dmsgC *dmsg.Client, discPK cipher.PubKey, log *logging.Logger) dmsgdisc.APIClient {
	dmsgURL := fmt.Sprintf("http://%s:%d", discPK.Hex(), dmsg.DefaultDmsgHTTPPort)
	tr := dmsghttp.MakeHTTPTransport(context.Background(), dmsgC)
	return dmsgdisc.NewHTTP(dmsgURL, &http.Client{Transport: tr}, log)
}

// initDmsgServer optionally runs a dmsg SERVER in-process under the visor's
// own PK/SK, reusing the visor's existing dmsg client (v.dmsgC) for discovery
// instead of standing up a second transit client. Config-gated and default
// off: it is a no-op unless Dmsg.Server.Enabled is set. The whole point is
// shared identity + a single dmsg client — so a visor can also be a dmsg
// server without a redundant transit client. The client-side self-session
// guard (SkipSelfServer, wired in dmsgc.New) keeps v.dmsgC from dialing this
// co-resident server.
// dmsgWSTLSCacheDirName is where a folded dmsg server keeps its autocert
// certificate cache when ws_tls_cache_dir is unset: beside the visor config,
// which is where the standalone server this fold replaced already kept it.
const dmsgWSTLSCacheDirName = "dmsg-autocert"

func initDmsgServer(ctx context.Context, v *Visor, log *logging.Logger) error {
	if v.conf.Dmsg == nil || v.conf.Dmsg.Server == nil || !v.conf.Dmsg.Server.Enabled {
		return nil
	}
	srvCfg := v.conf.Dmsg.Server

	// A standalone dmsg-server config file: run the whole service in-process
	// (own key, wss, health, route-setup surfaces) — nothing below applies.
	if srvCfg.ConfigPath != "" {
		return initDmsgServerFromFile(ctx, v, log, srvCfg.ConfigPath)
	}

	dmsgC := v.dmsgC
	if dmsgC == nil {
		return nil
	}

	// Wait for the visor's dmsg client to be ready — the server's discovery
	// registration rides its sessions.
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-dmsgC.Ready():
	}

	// Discovery PK for dmsg-only registration: the visor's dmsg discovery
	// URL is dmsg://<pk>:port. Without a resolvable discovery PK the server
	// can't register over dmsg; log and no-op rather than fail visor init.
	discPK := cmdutil.PKFromDmsgURL(v.conf.Dmsg.DiscoveryDmsg)
	if discPK == (cipher.PubKey{}) {
		discPK = cmdutil.PKFromDmsgURL(v.conf.Dmsg.Discovery)
	}
	if discPK == (cipher.PubKey{}) {
		log.Warn("in-process dmsg server enabled but no dmsg discovery PK (Dmsg.DiscoveryDmsg); not starting")
		return nil
	}

	// Share the visor's transport TCP port unless the operator pinned this
	// server to an address of its own. The shared branch comes from the same
	// cmux that already splits stcpr from WS, so the server needs no port of
	// its own and nothing new has to be opened on the host or forwarded.
	lis := v.dmsgSharedLis
	shared := lis != nil
	localAddr := srvCfg.LocalAddress
	if !shared {
		if localAddr == "" {
			localAddr = ":8081"
		}
	}

	srvConf := dmsg.DefaultServerConfig()
	// Advertise the binary's version, as the standalone service does. Without
	// it the entry carries disc.currentVersion ("0.0.1") and the server is
	// indistinguishable in discovery from any other, so there is no way to see
	// which servers have picked up a fix.
	srvConf.Version = dmsgsrv.EntryVersion()
	srv := dmsg.NewServer(v.conf.PK, v.conf.SK, dmsgOnlyDisc(dmsgC, discPK, log), srvConf, dmsgmetrics.NewEmpty())
	srv.SetLogger(log)

	if !shared {
		var err error
		if lis, err = net.Listen("tcp", localAddr); err != nil {
			return fmt.Errorf("in-process dmsg server: listen on %s: %w", localAddr, err)
		}
	}
	if localAddr == "" {
		localAddr = lis.Addr().String()
	}

	go func() {
		// srv.Serve blocks until the server is closed; PublicAddress is the
		// advertised address ("" = advertise whatever the listener resolves).
		if serr := srv.Serve(lis, srvCfg.PublicAddress); serr != nil && !errors.Is(serr, dmsg.ErrClosed) {
			log.WithError(serr).Warn("in-process dmsg server stopped")
		}
	}()

	log.WithField("local_pk", v.conf.PK).
		WithField("local_address", localAddr).
		WithField("public_address", srvCfg.PublicAddress).
		WithField("shared_transport_port", shared).
		Info("Started in-process dmsg server on the visor key")

	// Serve dmsg-over-QUIC on the transport port's UDP side. The standalone
	// server bound that port itself; folded into a visor, the port belongs to
	// the visor's shared QUIC socket, which refused the dmsg ALPN — so every
	// client's QUIC dial failed at the handshake ("tls: internal error") and
	// fell back to TCP. Advertised on the public address's host, as the
	// standalone server does.
	if shared && v.dmsgFactory != nil {
		udpAddr, qerr := v.dmsgFactory.SetDmsgQUICServer(srv)
		switch {
		case qerr != nil:
			log.WithError(qerr).Warn("dmsg over QUIC unavailable on the shared transport port")
		case udpAddr != nil:
			if adv := dmsgQUICAdvertisedAddr(srvCfg.PublicAddress, udpAddr); adv != "" {
				srv.AdvertiseQUIC(adv)
				log.WithField("addr_udp", adv).Info("Serving dmsg over QUIC on the shared transport port")
			}
		}
		// ...and WebTransport, which browsers dial with the certificate hash
		// pinned, beside the visor's own WT transport on the same socket. The
		// certificate rotates before browsers stop accepting it, and each new
		// hash is advertised as it is made.
		_, werr := v.dmsgFactory.SetDmsgWTServer(srv, func(wtAddr net.Addr, wtHash, wtNext [32]byte) {
			adv := dmsgQUICAdvertisedAddr(srvCfg.PublicAddress, wtAddr)
			if adv == "" {
				return
			}
			wtURL := "https://" + adv + dmsg.WTPath
			srv.AdvertiseWT(wtURL, wtHash, wtNext)
			log.WithField("addr_wt", wtURL).WithField("cert_hash", hex.EncodeToString(wtHash[:])).
				Info("Serving dmsg over WebTransport on the shared transport port")
		})
		if werr != nil {
			log.WithError(werr).Warn("dmsg over WebTransport unavailable on the shared transport port")
		}
	}

	// Restore the WebSocket front. A standalone dmsg server served
	// dmsg-over-WS on its own main port and advertised
	// wss://<DNSLabel>.<suffix>/dmsg, which is the ONLY way a browser or wasm
	// visor can reach it. Folded into a visor the server shares the transport
	// port, whose HTTP/1 cmux branch belongs to the WS transport — so the WS
	// front silently disappeared from every folded server, and browser visors
	// were left with just the hosts that had never been folded.
	//
	// The port is unchanged by the fold (transport_port is pinned to the port
	// the server already used), so the DNS record still points at the right
	// place — but the TLS front did NOT survive it; see below.
	if shared && v.dmsgFactory != nil {
		suffix := strings.TrimPrefix(deployment.Prod.WSSDomainSuffix, ".")
		// Gate on the deployment knowing this key, exactly as the standalone
		// service does: a third party running this binary must never advertise
		// the deployment's domain for a PK with no DNS record.
		if suffix != "" && deployment.Prod.IsKnownDmsgServer(v.conf.PK) {
			wssHost := v.conf.PK.DNSLabel() + "." + suffix
			wssURL := "wss://" + wssHost + "/dmsg"
			v.dmsgFactory.SetDmsgWSHandler(srv.WSHandler(wssURL))
			log.WithField("ws_url", wssURL).
				Info("Serving dmsg over WebSocket on the shared transport port")

			// ...and, where the host has no front of its own, the TLS that
			// makes that URL dialable. Whether one exists differs per host:
			// some already run Caddy on :443, and on the rest the terminator
			// was the standalone dmsg-server's OWN autocert listener, which
			// the fold retired along with the process that owned it. So
			// restoring the WS route alone left those hosts advertising a wss
			// front that refuses the connection. ws_tls_address says which
			// kind of host this is, exactly as it does in a standalone
			// dmsg-server config; empty keeps the external front.
			if tlsAddr := srvCfg.WSTLSAddress; tlsAddr != "" {
				cacheDir := srvCfg.WSTLSCacheDir
				if cacheDir == "" {
					cacheDir = dmsgWSTLSCacheDirName
					if p := v.conf.Path(); p != "" {
						cacheDir = filepath.Join(filepath.Dir(p), dmsgWSTLSCacheDirName)
					}
				}
				if tlsLis := dmsgsrv.ServeWSTLS(log, srv, tlsAddr, cacheDir, wssHost, wssURL); tlsLis != nil {
					v.pushCloseStack("dmsg_server_wss", tlsLis.Close)
				}
			}
		}
	}
	v.dmsgSrv.Store(srv)
	v.dmsgSrvRole.Store(&visorapi.DmsgServerRole{
		Mode:                dmsgServerModeOwnKey,
		PK:                  v.conf.PK,
		OwnKey:              true,
		SharedTransportPort: shared,
		LocalAddress:        localAddr,
		PublicAddress:       srvCfg.PublicAddress,
		StartedAt:           time.Now(),
	})

	v.pushCloseStack("dmsg_server", func() error {
		v.dmsgSrvRole.Store(nil)
		v.dmsgSrv.Store(nil)
		cerr := srv.Close()
		// Only a listener this server owns is closed here. The shared branch
		// belongs to the transport cmux, which stcpr and WS are still serving;
		// closing it is the transport stage's job.
		if shared {
			return cerr
		}
		if lerr := lis.Close(); lerr != nil && !errors.Is(lerr, net.ErrClosed) && cerr == nil {
			cerr = lerr
		}
		return cerr
	})

	return nil
}

// initDmsgServerFromFile runs the standalone dmsg-server service from its
// config file inside the visor process. The service builds its own transit
// dmsg client under the server's key, so it neither shares nor collides with
// the visor's client; it is stopped through the close stack.
func initDmsgServerFromFile(_ context.Context, v *Visor, log *logging.Logger, path string) error {
	svc := dmsgsrv.New(&dmsgsrv.Config{ConfigPath: path}, log)
	runCtx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		if err := svc.Run(runCtx); err != nil && !errors.Is(err, context.Canceled) {
			log.WithError(err).Error("in-process dmsg server (config file) stopped")
		}
	}()
	log.WithField("config_path", path).Info("Started in-process dmsg server from config file")

	role := &visorapi.DmsgServerRole{
		Mode:       dmsgServerModeConfigPath,
		ConfigPath: path,
		StartedAt:  time.Now(),
	}
	// The service holds the config privately; re-read the file for the public
	// half of it (key and addresses — never the secret key) so `visor state`
	// can name the key this server registers under, which is NOT the visor's.
	if cfg, lerr := dmsgsrv.LoadFile(path); lerr != nil {
		log.WithError(lerr).Debug("in-process dmsg server: config unreadable for state reporting")
	} else {
		role.PK = cfg.PubKey
		role.LocalAddress = cfg.LocalAddress
		role.PublicAddress = cfg.PublicAddress
	}
	v.dmsgSrvRole.Store(role)

	v.pushCloseStack("dmsg_server", func() error {
		v.dmsgSrvRole.Store(nil)
		cancel()
		<-done
		return nil
	})
	return nil
}

// dmsgQUICAdvertisedAddr is the QUIC endpoint a folded dmsg server advertises:
// the public address's host on the shared UDP socket's port. Empty when there
// is no public host to advertise.
func dmsgQUICAdvertisedAddr(publicAddr string, udpAddr net.Addr) string {
	host, _, err := net.SplitHostPort(publicAddr)
	if err != nil || host == "" {
		return ""
	}
	ua, ok := udpAddr.(*net.UDPAddr)
	if !ok || ua.Port == 0 {
		return ""
	}
	return net.JoinHostPort(host, strconv.Itoa(ua.Port))
}
