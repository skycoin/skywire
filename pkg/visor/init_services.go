// init_services.go contains service initialization logic: health tracking, uptime, survey,
// forwarding connections, ping listeners, and the UI server.
package visor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/skycoin/skywire/pkg/app/appevent"
	"github.com/skycoin/skywire/pkg/app/appnet"
	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/dmsg/dmsgcurl"
	"github.com/skycoin/skywire/pkg/geo"
	"github.com/skycoin/skywire/pkg/geoip"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/skyenv"
	"github.com/skycoin/skywire/pkg/tpviz"
	"github.com/skycoin/skywire/pkg/transport"
	types "github.com/skycoin/skywire/pkg/transport/types"
	"github.com/skycoin/skywire/pkg/utclient"
)

func initEventBroadcaster(ctx context.Context, v *Visor, log *logging.Logger) error { //nolint:revive
	const ebcTimeout = time.Second
	ebc := appevent.NewBroadcaster(log, ebcTimeout)
	v.pushCloseStack("event_broadcaster", ebc.Close)

	v.initLock.Lock()
	v.ebc = ebc
	v.initLock.Unlock()
	return nil
}

func initSystemSurvey(_ context.Context, v *Visor, log *logging.Logger) error {
	go GenerateSurvey(v, log, true) //nolint:gosec
	return nil
}

func initUptimeTracker(ctx context.Context, v *Visor, log *logging.Logger) error {
	const tickDuration = 5 * time.Minute

	conf := v.conf.UptimeTracker

	if conf == nil {
		v.log.Debug("'uptime_tracker' is not configured, skipping.")
		return nil
	}

	// Resolve UT URL: prefer HTTP, fall back to dmsghttp.
	utURL := conf.Addr
	if utURL == "" && conf.AddrDmsg != "" {
		utURL = conf.AddrDmsg
	}
	if utURL == "" {
		v.log.Debug("'uptime_tracker' addr is empty, skipping.")
		return nil
	}

	httpC, err := getHTTPClient(ctx, v, utURL)
	if err != nil {
		return err
	}

	pIP, err := getPublicIP(v, utURL)
	if err != nil {
		return err
	}

	ut, err := utclient.NewHTTP(utURL, v.conf.PK, v.conf.SK, httpC, pIP, v.MasterLogger())
	if err != nil {
		v.log.WithError(err).Warn("Failed to connect to uptime tracker.")
		return nil
	}

	// Also create a heartbeat client for the TPD so visors without
	// transports still report uptime. The TPD's /v4/update endpoint
	// uses the same auth + protocol as the uptime tracker.
	var tpdUT utclient.APIClient
	tpdURL := v.conf.Transport.Discovery
	if tpdURL == "" && v.conf.Transport.DiscoveryDmsg != "" {
		tpdURL = v.conf.Transport.DiscoveryDmsg
	}
	if tpdURL != "" {
		tpdHTTP, err := getHTTPClient(ctx, v, tpdURL)
		if err != nil {
			log.WithError(err).Warn("Failed to create TPD heartbeat client")
		} else {
			tpdPIP, _ := getPublicIP(v, tpdURL) //nolint:errcheck
			tpdClient, err := utclient.NewHTTP(tpdURL, v.conf.PK, v.conf.SK, tpdHTTP, tpdPIP, v.MasterLogger())
			if err != nil {
				log.WithError(err).Warn("Failed to connect to TPD for heartbeat")
			} else {
				tpdUT = tpdClient
			}
		}
	}

	ticker := time.NewTicker(tickDuration)

	go func() { //nolint:gosec
		for range ticker.C {
			c := context.Background()
			if err := ut.UpdateVisorUptime(c, v.conf.Version); err != nil {
				v.isServicesHealthy.unset()
				v.isUptimeTrackerHealthy.unset()
				log.WithError(err).Warn("Failed to update visor uptime.")
			} else {
				v.isServicesHealthy.set()
				v.isUptimeTrackerHealthy.set()
			}
			// Heartbeat to TPD for integrated uptime tracking.
			// Errors are non-fatal — transport re-registration also
			// records heartbeats, so this is a backup for transportless visors.
			if tpdUT != nil {
				if err := tpdUT.UpdateVisorUptime(c, v.conf.Version); err != nil {
					log.WithError(err).Debug("Failed to send TPD heartbeat")
				}
			}
		}
	}()

	v.pushCloseStack("uptime_tracker", func() error {
		ticker.Stop()
		return nil
	})

	v.initLock.Lock()
	v.uptimeTracker = ut
	v.initLock.Unlock()

	return nil
}

func getPublicIP(v *Visor, service string) (string, error) {
	var serviceURL dmsgcurl.URL
	var pIP string
	err := serviceURL.Fill(service)
	// only get the IP if the url is of dmsg
	// else just send empty string as ip
	if serviceURL.Scheme != "dmsg" {
		return pIP, nil
	}
	if err != nil {
		return pIP, fmt.Errorf("provided URL is invalid: %w", err)
	}

	<-v.stun.ready
	if v.stun.client.PublicIP != nil {
		pIP = v.stun.client.PublicIP.IP()
		return pIP, nil
	}
	return pIP, fmt.Errorf("cannot fetch public ip")
}

// GeoData holds geolocation information for the visor.
type GeoData struct {
	CountryCode string  `json:"country_code,omitempty"`
	CountryName string  `json:"country_name,omitempty"`
	RegionCode  string  `json:"region_code,omitempty"`
	RegionName  string  `json:"region_name,omitempty"`
	CityName    string  `json:"city_name,omitempty"`
	Latitude    float64 `json:"latitude,omitempty"`
	Longitude   float64 `json:"longitude,omitempty"`
}

// LookupGeo returns geolocation data for the given IP using the embedded
// MaxMind GeoLite2-City database. Returns nil if ip is empty or lookup
// fails. No network round-trip — replaces the previous HTTP call to the
// configured ip.skycoin.com geoip service.
func LookupGeo(ip string) *GeoData {
	if ip == "" {
		return nil
	}
	db, err := geoip.OpenEmbedded()
	if err != nil {
		return nil
	}
	defer db.Close() //nolint:errcheck
	res, err := geoip.Lookup(db, ip)
	if err != nil {
		return nil
	}
	g := &GeoData{
		CountryCode: res.CountryCode,
		CountryName: res.CountryName,
		RegionCode:  res.RegionCode,
		RegionName:  res.RegionName,
		CityName:    res.CityName,
	}
	if res.Latitude != nil {
		g.Latitude = *res.Latitude
	}
	if res.Longitude != nil {
		g.Longitude = *res.Longitude
	}
	return g
}

// serviceGeo returns a snapshot of the visor's cached geolocation in
// the shape expected by service-discovery / uptime-tracker entries.
// When the visor populates this on its registrations, the receiving
// service short-circuits its own IP→geo HTTP lookup. Returns nil
// when geolocation has not been determined yet (e.g. before the
// address-resolver init that populates v.geo.data).
func (v *Visor) serviceGeo() *geo.LocationData {
	v.geo.mu.RLock()
	defer v.geo.mu.RUnlock()
	if v.geo.data == nil {
		return nil
	}
	return &geo.LocationData{
		Country: v.geo.data.CountryCode,
		Region:  v.geo.data.RegionCode,
		Lat:     v.geo.data.Latitude,
		Lon:     v.geo.data.Longitude,
	}
}

func initSkywireForwardConn(ctx context.Context, v *Visor, log *logging.Logger) error {
	ctx, cancel := context.WithCancel(ctx)
	// waiting for at least one transport to initialize
	<-v.tpM.Ready()
	connApp := appnet.Addr{
		Net:    appnet.TypeSkynet,
		PubKey: v.conf.PK,
		Port:   routing.Port(skyenv.SkyForwardingServerPort),
	}
	l, err := appnet.ListenContext(ctx, connApp)
	if err != nil {
		cancel()
		return err
	}

	v.pushCloseStack("sky_forwarding", func() error {
		cancel()
		if cErr := l.Close(); cErr != nil {
			log.WithError(cErr).Error("Error closing listener.")
		}
		return nil
	})

	go func() {
		for {
			log.Debug("Accepting sky forwarding conn...")
			conn, err := l.Accept()
			if err != nil {
				if errors.Is(err, appnet.ErrClosedConn) {
					return
				}
				log.WithError(err).Warn("Failed to accept forwarding conn, continuing")
				continue
			}
			log.Debug("Accepted sky forwarding conn")

			v.pushCloseStack("sky_forwarding", func() error {
				cancel()
				if cErr := conn.Close(); cErr != nil {
					log.WithError(cErr).Error("Error closing conn.")
				}
				return nil
			})

			log.Debug("Wrapping conn...")
			wrappedConn, err := appnet.WrapConn(conn)
			if err != nil {
				log.WithError(err).Warn("Failed to wrap forwarding conn, continuing")
				conn.Close() //nolint:errcheck,gosec
				continue
			}

			rAddr := wrappedConn.RemoteAddr().(appnet.Addr)
			log.Debugf("Accepted sky forwarding conn on %s from %s", wrappedConn.LocalAddr(), rAddr.PubKey)
			go func() {
				defer func() {
					if r := recover(); r != nil {
						log.Errorf("Panic in server conn handler: %v", r)
					}
				}()
				handleServerConn(log, wrappedConn, v)
			}()
		}
	}()

	// Also accept direct transport connections via VStreamMux (route ID 0).
	// This allows peers with a direct transport to skip route setup entirely.
	log.WithField("tpM_nil", v.tpM == nil).Debug("Checking transport manager for VStreamMux")
	if v.tpM != nil {
		skynetMux := transport.NewVStreamMux(v.tpM, routing.SkynetForwardPacket, log)
		v.tpM.SetSkynetForwardHandler(skynetMux.HandlePacket)
		v.skynetFwdMux = skynetMux
		go func() {
			for {
				stream, err := skynetMux.Accept()
				if err != nil {
					return
				}
				log.WithField("remote", stream.RemotePK().String()).
					Debug("Accepted direct skynet forwarding stream")
				go func() {
					defer func() {
						if r := recover(); r != nil {
							log.Errorf("Panic in direct skynet handler: %v", r)
						}
					}()
					conn := &vstreamConn{VStream: stream}
					log.Debug("Direct skynet: calling handleServerConn")
					handleServerConn(log, conn, v)
					log.Debug("Direct skynet: handleServerConn returned")
				}()
			}
		}()
		log.Info("Direct skynet forwarding enabled (route ID 0)")
	}

	// Bring up the AppDirect mux for direct skywire-network app dials
	// (skysocks-client, vpn-client, etc. — anything riding the
	// appnet.SkywireNetworker dial path). Mirrors the skynet mux
	// above: visor owns the mux, the SkywireNetworker registers an
	// accept loop on it, and clients use it before falling back to
	// route-setup-mediated DialRoutes. Hands the same mux to the
	// networker for outbound dials too.
	if v.tpM != nil {
		appDirectMux := transport.NewVStreamMux(v.tpM, routing.AppDirectPacket, log)
		v.tpM.SetAppDirectHandler(appDirectMux.HandlePacket)
		v.appDirectMux = appDirectMux
		if n, err := appnet.ResolveNetworker(appnet.TypeSkynet); err == nil {
			if sn, ok := n.(*appnet.SkywireNetworker); ok {
				sn.SetAppDirectMux(appDirectMux)
				log.Info("Direct skywire-network app dial enabled (route ID 0)")
			}
		}
	}

	return nil
}

// remotePKFromForwardingConn extracts the remote visor PK from a conn
// delivered through the skynet sky-forwarding path. Both delivery paths
// (appnet-wrapped route conns and direct VStream conns) carry the PK;
// this normalizes the access so callers don't need to switch on the
// underlying type. Returns ok=false only if neither route works.
func remotePKFromForwardingConn(c net.Conn) (cipher.PubKey, bool) {
	if pkp, ok := c.(interface{ RemotePK() cipher.PubKey }); ok {
		return pkp.RemotePK(), true
	}
	if a, ok := c.RemoteAddr().(appnet.Addr); ok {
		return a.PubKey, true
	}
	return cipher.PubKey{}, false
}

// vstreamConn wraps a VStream to implement net.Conn.
type vstreamConn struct {
	*transport.VStream
}

func (c *vstreamConn) LocalAddr() net.Addr {
	return &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0}
}
func (c *vstreamConn) RemoteAddr() net.Addr {
	return &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0}
}
func (c *vstreamConn) SetDeadline(_ time.Time) error      { return nil }
func (c *vstreamConn) SetReadDeadline(_ time.Time) error  { return nil }
func (c *vstreamConn) SetWriteDeadline(_ time.Time) error { return nil }

func handleServerConn(log *logging.Logger, remoteConn net.Conn, v *Visor) {
	// Send ready signal to synchronize with client after noise handshake
	// This ensures the noise handshake is complete before data exchange
	if _, err := remoteConn.Write([]byte{0x00}); err != nil {
		log.WithError(err).Error("Failed to send ready signal")
		return
	}
	log.Debug("Sent ready signal to client")

	buf := make([]byte, 32*1024)
	n, err := remoteConn.Read(buf)
	if err != nil {
		log.WithError(err).Error("Failed to read packet")
		return
	}

	var cMsg clientMsg
	err = json.Unmarshal(buf[:n], &cMsg)
	if err != nil {
		log.WithError(err).Error("Failed to marshal json")
		sendError(log, remoteConn, err)
		return
	}
	log.Debugf("Received ClientMsg: port=%d (raw JSON: %s)", cMsg.Port, string(buf[:n]))

	// First check the service registry — this is the preferred path
	// that dispatches directly to the handler without a localhost
	// TCP bounce. Falls back to the legacy localhost-dial path for
	// ports registered via RegisterTCPPort (backward compat for
	// user-managed forwarded ports).
	if cMsg.Port > 0 && cMsg.Port <= 65535 {
		if handler, ok := v.services.Get(uint16(cMsg.Port)); ok { //nolint:gosec
			log.Debugf("Dispatching port %v via service registry", cMsg.Port)
			sendError(log, remoteConn, nil)
			go func() {
				defer func() {
					if r := recover(); r != nil {
						log.Errorf("Panic in service handler: %v", r)
					}
				}()
				handler(remoteConn)
			}()
			return
		}
	}

	// Legacy path: check if port is registered for localhost TCP
	// forwarding (user-managed ports via RegisterTCPPort / CLI).
	ok := isPortRegistered(cMsg.Port, v)
	if !ok {
		log.Errorf("Port :%v not registered", cMsg.Port)
		sendError(log, remoteConn, fmt.Errorf("port :%v not registered", cMsg.Port))
		return
	}

	// ProxyAddr wins when set so users can forward to an arbitrary
	// IP:port instead of localhost:port. Default falls back to
	// localhost:cMsg.Port for entries that don't override the target.
	lHost := fmt.Sprintf("localhost:%v", cMsg.Port)
	fp := v.forwardedPorts.Get(cMsg.Port)
	if fp != nil && fp.ProxyAddr != "" {
		lHost = fp.ProxyAddr
	}

	// Enforce per-port PK whitelist if one is set on the forwarded port.
	// Fail-closed when a whitelist is set but the peer PK can't be
	// determined — the alternative would silently bypass the gate.
	if fp != nil && len(fp.Whitelist) > 0 {
		remotePK, pkOK := remotePKFromForwardingConn(remoteConn)
		if !pkOK {
			log.WithField("port", cMsg.Port).
				Warn("Rejected: cannot identify peer on whitelisted port")
			sendError(log, remoteConn, fmt.Errorf("port :%v: cannot verify peer", cMsg.Port))
			return
		}
		if !v.isPeerAllowed(cMsg.Port, remotePK) {
			log.WithField("peer", remotePK).WithField("port", cMsg.Port).
				Warn("Rejected: peer not in whitelist")
			sendError(log, remoteConn, fmt.Errorf("port :%v: not whitelisted", cMsg.Port))
			return
		}
	}

	// Skip the local-listener check when ProxyAddr points elsewhere —
	// the target host:port may not even be on this machine. The dial in
	// forwardRawTCP will surface a real failure to the peer.
	if fp == nil || fp.ProxyAddr == "" {
		ok = isPortAvailable(log, cMsg.Port)
		if ok {
			log.Errorf("Failed to dial port %v", cMsg.Port)
			sendError(log, remoteConn, fmt.Errorf("failed to dial port %v", cMsg.Port))
			return
		}
	}

	log.Debugf("Forwarding %s via localhost TCP", lHost)

	// send nil error to indicate to the remote connection that everything is ok
	sendError(log, remoteConn, nil)

	go func() {
		defer func() {
			if r := recover(); r != nil {
				log.Errorf("Panic in forward handler: %v", r)
			}
		}()
		forwardRawTCP(log, remoteConn, lHost)
	}()
}

// forwardRawTCP does bidirectional raw TCP proxying using io.Copy
func forwardRawTCP(log *logging.Logger, remoteConn net.Conn, lHost string) {
	localConn, err := net.Dial("tcp", lHost)
	if err != nil {
		log.WithError(err).Error("Failed to dial local server")
		closeConn(log, remoteConn)
		return
	}
	log.WithField("local", lHost).Debug("forwardRawTCP: connected to local server")

	done := make(chan struct{}, 2)

	// remote -> local
	go func() {
		n, err := io.Copy(localConn, remoteConn)
		log.WithField("bytes", n).WithError(err).Debug("forwardRawTCP: remote->local ended")
		done <- struct{}{}
	}()

	// local -> remote
	go func() {
		n, err := io.Copy(remoteConn, localConn)
		log.WithField("bytes", n).WithError(err).Debug("forwardRawTCP: local->remote ended")
		done <- struct{}{}
	}()

	// Wait for one direction to finish, then close both
	<-done
	closeConn(log, remoteConn)
	closeConn(log, localConn)
	<-done
}

func sendError(log *logging.Logger, remoteConn net.Conn, sendErr error) {
	var sReply serverReply
	if sendErr != nil {
		sErr := sendErr.Error()
		sReply = serverReply{
			Error: &sErr,
		}
	}

	srvReply, err := json.Marshal(sReply)
	if err != nil {
		log.WithError(err).Error("Failed to unmarshal json")
	}

	_, err = remoteConn.Write(srvReply)
	if err != nil {
		log.WithError(err).Error("Failed write server msg")
	}

	log.Debugf("Server reply sent %s", srvReply)
	// close conn if we send an error
	if sendErr != nil {
		closeConn(log, remoteConn)
	}
}

func closeConn(log *logging.Logger, conn net.Conn) {
	if err := conn.Close(); err != nil {
		log.WithError(err).Errorf("Error closing client %s connection", conn.RemoteAddr())
	}
}

type clientMsg struct {
	Port int `json:"port"`
}

type serverReply struct {
	Error *string `json:"error,omitempty"`
}

func initPing(ctx context.Context, v *Visor, log *logging.Logger) error {
	ctx, cancel := context.WithCancel(ctx)
	// waiting for at least one transport to initialize
	<-v.tpM.Ready()

	connApp := appnet.Addr{
		Net:    appnet.TypeSkynet,
		PubKey: v.conf.PK,
		Port:   routing.Port(skyenv.SkyPingPort),
	}

	l, err := appnet.ListenContext(ctx, connApp)
	if err != nil {
		cancel()
		return err
	}

	v.pushCloseStack("skywire_ping", func() error {
		cancel()
		if cErr := l.Close(); cErr != nil {
			log.WithError(cErr).Error("Error closing listener.")
		}
		return nil
	})

	go func() {
		for {
			log.Debug("Accepting sky ping conn...")
			conn, err := l.Accept()
			if err != nil {
				if errors.Is(err, appnet.ErrClosedConn) {
					return
				}
				log.WithError(err).Warn("Failed to accept ping conn, continuing")
				continue
			}
			log.Debug("Accepted sky ping conn")
			log.Debug("Wrapping conn...")
			wrappedConn, err := appnet.WrapConn(conn)
			if err != nil {
				log.WithError(err).Warn("Failed to wrap ping conn, continuing")
				conn.Close() //nolint:errcheck,gosec
				continue
			}

			rAddr := wrappedConn.RemoteAddr().(appnet.Addr)
			log.Debugf("Accepted sky ping conn on %s from %s", wrappedConn.LocalAddr(), rAddr.PubKey)
			go handlePingConn(log, wrappedConn, v)
		}
	}()
	return nil
}

func handlePingConn(log *logging.Logger, remoteConn net.Conn, _ *Visor) {
	defer func() {
		if err := remoteConn.Close(); err != nil {
			log.WithError(err).Debug("Error closing ping conn")
		}
	}()
	for {
		buf := make([]byte, 32*1024)
		n, err := remoteConn.Read(buf)
		if err != nil {
			if !errors.Is(err, io.EOF) {
				log.WithError(err).Error("Failed to read ping packet")
			}
			return
		}
		var size PingSizeMsg
		err = json.Unmarshal(buf[:n], &size)
		if err != nil {
			log.WithError(err).Error("Failed to unmarshal ping size")
			return
		}

		// Ack the size message
		_, err = remoteConn.Write([]byte("ok"))
		if err != nil {
			log.WithError(err).Error("Failed to write ping ack")
			return
		}

		// Read the full ping payload
		var ping []byte
		for len(ping) < size.Size {
			n, err = remoteConn.Read(buf)
			if err != nil {
				if !errors.Is(err, io.EOF) {
					log.WithError(err).Error("Failed to read ping data")
				}
				return
			}
			ping = append(ping, buf[:n]...)
		}

		// Echo back for RTT measurement
		// If EchoFull is set, echo the full payload for bandwidth testing
		if size.EchoFull {
			_, err = remoteConn.Write(ping)
			if err != nil {
				log.WithError(err).Error("Failed to write full ping echo")
				return
			}
			log.Debugf("Echoed full ping response (%d bytes)", len(ping))
		} else {
			_, err = remoteConn.Write([]byte("pong"))
			if err != nil {
				log.WithError(err).Error("Failed to write ping echo")
				return
			}
			log.Debug("Echoed ping response")
		}
	}
}

// initLatencyProbe starts a listener on LatencyProbePort (46) to accept
// transport latency measurement routes. The RouteGroup automatically handles
// ping/pong packets - we just need to keep the connection alive.
func initLatencyProbe(ctx context.Context, v *Visor, log *logging.Logger) error {
	ctx, cancel := context.WithCancel(ctx)
	// Wait for at least one transport to initialize
	<-v.tpM.Ready()

	connApp := appnet.Addr{
		Net:    appnet.TypeSkynet,
		PubKey: v.conf.PK,
		Port:   routing.Port(skyenv.LatencyProbePort),
	}

	l, err := appnet.ListenContext(ctx, connApp)
	if err != nil {
		cancel()
		return err
	}

	v.pushCloseStack("latency_probe", func() error {
		cancel()
		if cErr := l.Close(); cErr != nil {
			log.WithError(cErr).Error("Error closing latency probe listener.")
		}
		return nil
	})

	go func() {
		for {
			log.Debug("Accepting latency probe conn...")
			conn, err := l.Accept()
			if err != nil {
				if errors.Is(err, appnet.ErrClosedConn) {
					return
				}
				log.WithError(err).Warn("Failed to accept latency probe conn, continuing")
				continue
			}
			log.Debug("Accepted latency probe conn")

			// Handle the connection in a goroutine.
			// The RouteGroup handles ping/pong packets automatically.
			// We just need to keep the connection alive until the initiator closes it.
			go handleLatencyProbeConn(log, conn)
		}
	}()
	return nil
}

// handleLatencyProbeConn keeps a latency probe connection alive.
// The RouteGroup automatically responds to ping packets with pong packets.
// This handler just waits for the connection to close.
func handleLatencyProbeConn(log *logging.Logger, conn net.Conn) {
	defer func() {
		if err := conn.Close(); err != nil {
			log.WithError(err).Debug("Error closing latency probe conn")
		}
	}()

	// Read in a loop to detect when the connection is closed.
	// The RouteGroup handles ping/pong packets at a lower level.
	buf := make([]byte, 1024)
	for {
		_, err := conn.Read(buf)
		if err != nil {
			if !errors.Is(err, io.EOF) && !errors.Is(err, net.ErrClosed) {
				log.WithError(err).Debug("Latency probe conn closed")
			}
			return
		}
	}
}

// visorAPIAdapter adapts *Visor to the tpviz.VisorAPI interface.
// It converts between visor types and tpviz types.
type visorAPIAdapter struct {
	v *Visor
}

// Overview implements tpviz.VisorAPI.
func (a *visorAPIAdapter) Overview() (*tpviz.VisorOverview, error) {
	ov, err := a.v.Overview()
	if err != nil {
		return nil, err
	}

	// Convert visor.Overview to tpviz.VisorOverview
	result := &tpviz.VisorOverview{
		PubKey:      ov.PubKey,
		RoutesCount: ov.RoutesCount,
	}

	// Convert transports
	for _, tp := range ov.Transports {
		result.Transports = append(result.Transports, &tpviz.TransportSummary{
			ID:      tp.ID,
			Local:   tp.Local,
			Remote:  tp.Remote,
			Type:    tp.Type,
			Log:     tp.Log,
			IsSetup: tp.IsSetup,
			Label:   tp.Label,
		})
	}

	return result, nil
}

// RoutingRules implements tpviz.VisorAPI.
func (a *visorAPIAdapter) RoutingRules() ([]routing.Rule, error) {
	return a.v.RoutingRules()
}

// Close implements tpviz.VisorAPI.
func (a *visorAPIAdapter) Close() error {
	// Don't actually close the visor - the adapter doesn't own it
	return nil
}

// AddTransport implements tpviz.VisorAPI - creates a transport from the local visor to a remote visor.
func (a *visorAPIAdapter) AddTransport(ctx context.Context, remotePK, tpType string) (*tpviz.TransportSummary, error) {
	var remote cipher.PubKey
	if err := remote.UnmarshalText([]byte(remotePK)); err != nil {
		return nil, fmt.Errorf("invalid remote PK: %w", err)
	}

	netType := types.Type(tpType)
	if netType != types.STCPR && netType != types.SUDPH {
		return nil, fmt.Errorf("transport type must be stcpr or sudph")
	}

	tp, err := a.v.tpM.SaveTransport(ctx, remote, netType, transport.LabelSkycoin)
	if err != nil {
		return nil, err
	}

	return &tpviz.TransportSummary{
		ID:      tp.Entry.ID,
		Local:   a.v.conf.PK,
		Remote:  tp.Remote(),
		Type:    tp.Type(),
		IsSetup: true,
		Label:   tp.Entry.Label,
	}, nil
}

// RemoveTransport implements tpviz.VisorAPI - removes a transport from the local visor.
func (a *visorAPIAdapter) RemoveTransport(ctx context.Context, tpID string) error {
	id, err := uuid.Parse(tpID)
	if err != nil {
		return fmt.Errorf("invalid transport ID: %w", err)
	}

	a.v.tpM.DeleteTransport(id)
	return nil
}

// DMSGHealth performs a health check on a remote visor via DMSG.
// It dials the target visor on port 80 and fetches the /health endpoint.
func (a *visorAPIAdapter) DMSGHealth(ctx context.Context, pk string) (*tpviz.DMSGHealthResponse, error) {
	var targetPK cipher.PubKey
	if err := targetPK.UnmarshalText([]byte(pk)); err != nil {
		return nil, fmt.Errorf("invalid PK: %w", err)
	}

	dmsgC := a.v.dmsgC
	if dmsgC == nil {
		return nil, fmt.Errorf("DMSG client not available")
	}

	// Dial the target visor on port 80 (HTTP port)
	conn, err := dmsgC.Dial(ctx, dmsg.Addr{PK: targetPK, Port: 80})
	if err != nil {
		return &tpviz.DMSGHealthResponse{
			Status: "unreachable",
			Error:  fmt.Sprintf("dmsg dial failed: %v", err),
		}, nil
	}
	defer conn.Close() //nolint:errcheck

	// Create HTTP client over the DMSG connection
	client := &http.Client{
		Transport: &http.Transport{
			DialContext: func(_ context.Context, _, _ string) (net.Conn, error) {
				return conn, nil
			},
		},
		Timeout: 10 * time.Second,
	}

	// Make the health check request
	req, err := http.NewRequestWithContext(ctx, "GET", "http://dmsg/health", nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	resp, err := client.Do(req)
	if err != nil {
		return &tpviz.DMSGHealthResponse{
			Status: "error",
			Error:  fmt.Sprintf("HTTP request failed: %v", err),
		}, nil
	}
	defer resp.Body.Close() //nolint:errcheck

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return &tpviz.DMSGHealthResponse{
			Status: "error",
			Error:  fmt.Sprintf("read response: %v", err),
		}, nil
	}

	// Try to parse as JSON
	var healthResp map[string]interface{}
	if err := json.Unmarshal(body, &healthResp); err == nil {
		status := "healthy"
		if resp.StatusCode != http.StatusOK {
			status = "unhealthy"
		}
		buildInfo := ""
		if bi, ok := healthResp["build_info"].(string); ok {
			buildInfo = bi
		}
		return &tpviz.DMSGHealthResponse{
			Status:    status,
			BuildInfo: buildInfo,
			Message:   string(body),
		}, nil
	}

	// Return raw response
	return &tpviz.DMSGHealthResponse{
		Status:  "healthy",
		Message: string(body),
	}, nil
}

// Ping implements tpviz.VisorAPI - performs a ping to a remote visor via routes or DMSG.
// It handles dial, ping, and cleanup in a single call for UI convenience.
// localRoute: when true and using routes, use cached TPD data instead of querying route finder.
func (a *visorAPIAdapter) Ping(ctx context.Context, pk string, useDMSG, localRoute bool, tries, sizeKB int) (*tpviz.PingResponse, error) {
	var targetPK cipher.PubKey
	if err := targetPK.UnmarshalText([]byte(pk)); err != nil {
		return nil, fmt.Errorf("invalid PK: %w", err)
	}

	mode := "route"
	if useDMSG {
		mode = "dmsg"
	}
	if localRoute && !useDMSG {
		mode = "route (local)"
	}

	conf := PingConfig{
		PK:         targetPK,
		Tries:      tries,
		PcktSize:   sizeKB,
		LocalRoute: localRoute,
	}

	var latencies []time.Duration
	var err error

	if useDMSG {
		// DMSG ping: dial -> ping -> stop
		if err = a.v.DialDmsgPing(targetPK); err != nil {
			return &tpviz.PingResponse{
				Status: "error",
				Error:  fmt.Sprintf("dmsg dial failed: %v", err),
				Mode:   mode,
			}, nil
		}
		defer func() {
			_ = a.v.StopDmsgPing(targetPK) //nolint:errcheck
		}()

		latencies, err = a.v.DmsgPing(conf)
	} else {
		// Route ping: dial -> ping -> stop
		if err = a.v.DialPing(conf); err != nil {
			return &tpviz.PingResponse{
				Status: "error",
				Error:  fmt.Sprintf("route dial failed: %v", err),
				Mode:   mode,
			}, nil
		}
		defer func() {
			_ = a.v.StopPing(targetPK) //nolint:errcheck
		}()

		latencies, err = a.v.Ping(conf)
	}

	if err != nil {
		return &tpviz.PingResponse{
			Status: "error",
			Error:  fmt.Sprintf("ping failed: %v", err),
			Mode:   mode,
		}, nil
	}

	// Calculate statistics
	if len(latencies) == 0 {
		return &tpviz.PingResponse{
			Status:     "timeout",
			PacketLoss: 100.0,
			Mode:       mode,
		}, nil
	}

	var sum, minVal, maxVal time.Duration
	latencyMs := make([]int64, 0, len(latencies))
	minVal = latencies[0]
	maxVal = latencies[0]

	for _, lat := range latencies {
		sum += lat
		if lat < minVal {
			minVal = lat
		}
		if lat > maxVal {
			maxVal = lat
		}
		latencyMs = append(latencyMs, lat.Milliseconds())
	}

	avg := sum / time.Duration(len(latencies))
	packetLoss := float64(tries-len(latencies)) / float64(tries) * 100.0

	return &tpviz.PingResponse{
		Status:     "success",
		LatencyMs:  avg.Seconds() * 1000,
		Latencies:  latencyMs,
		MinMs:      minVal.Seconds() * 1000,
		MaxMs:      maxVal.Seconds() * 1000,
		AvgMs:      avg.Seconds() * 1000,
		PacketLoss: packetLoss,
		Mode:       mode,
	}, nil
}

// Apps implements tpviz.VisorAPI - returns the list of all apps and their status.
func (a *visorAPIAdapter) Apps() ([]*tpviz.AppState, error) {
	apps, err := a.v.Apps()
	if err != nil {
		return nil, err
	}

	result := make([]*tpviz.AppState, 0, len(apps))
	for _, app := range apps {
		result = append(result, &tpviz.AppState{
			Name:           app.Name,
			Status:         int(app.Status),
			DetailedStatus: app.DetailedStatus,
			AutoStart:      app.AutoStart,
			Port:           uint16(app.Port),
			Args:           app.Args,
		})
	}
	return result, nil
}

// StartApp implements tpviz.VisorAPI - starts an application.
func (a *visorAPIAdapter) StartApp(appName string) error {
	return a.v.StartApp(appName)
}

// StopApp implements tpviz.VisorAPI - stops an application.
func (a *visorAPIAdapter) StopApp(appName string) error {
	return a.v.StopApp(appName)
}

// SetAutoStart implements tpviz.VisorAPI - sets the auto-start flag for an app.
func (a *visorAPIAdapter) SetAutoStart(appName string, autoStart bool) error {
	return a.v.SetAutoStart(appName, autoStart)
}

// SetAppPK implements tpviz.VisorAPI - sets the server public key for an app.
func (a *visorAPIAdapter) SetAppPK(appName, pk string) error {
	var pubKey cipher.PubKey
	if err := pubKey.UnmarshalText([]byte(pk)); err != nil {
		return fmt.Errorf("invalid public key: %w", err)
	}
	return a.v.SetAppPK(appName, pubKey)
}

// tpsAPIAdapter adapts *Visor's embeddedTPS to the tpviz.TPSAPI interface.
type tpsAPIAdapter struct {
	v *Visor
}

// AddTransport implements tpviz.TPSAPI.
func (a *tpsAPIAdapter) AddTransport(ctx context.Context, targetPK, remotePK, tpType string) (*tpviz.TPSTransportResponse, error) {
	tps := a.v.embeddedTPS
	if tps == nil {
		return nil, fmt.Errorf("embedded TPS not running")
	}

	var target, remote cipher.PubKey
	if err := target.UnmarshalText([]byte(targetPK)); err != nil {
		return nil, fmt.Errorf("invalid target PK: %w", err)
	}
	if err := remote.UnmarshalText([]byte(remotePK)); err != nil {
		return nil, fmt.Errorf("invalid remote PK: %w", err)
	}

	res, err := tps.AddTransport(ctx, target, remote, types.Type(tpType))
	if err != nil {
		return nil, err
	}

	return &tpviz.TPSTransportResponse{
		ID:     res.ID.String(),
		Local:  res.Local.String(),
		Remote: res.Remote.String(),
		Type:   string(res.Type),
	}, nil
}

// RemoveTransport implements tpviz.TPSAPI.
func (a *tpsAPIAdapter) RemoveTransport(ctx context.Context, targetPK, tpID string) error {
	tps := a.v.embeddedTPS
	if tps == nil {
		return fmt.Errorf("embedded TPS not running")
	}

	var target cipher.PubKey
	if err := target.UnmarshalText([]byte(targetPK)); err != nil {
		return fmt.Errorf("invalid target PK: %w", err)
	}

	id, err := uuid.Parse(tpID)
	if err != nil {
		return fmt.Errorf("invalid transport ID: %w", err)
	}

	return tps.RemoveTransport(ctx, target, id)
}

// GetTransports implements tpviz.TPSAPI.
func (a *tpsAPIAdapter) GetTransports(ctx context.Context, targetPK string) ([]tpviz.TPSTransportResponse, error) {
	tps := a.v.embeddedTPS
	if tps == nil {
		return nil, fmt.Errorf("embedded TPS not running")
	}

	var target cipher.PubKey
	if err := target.UnmarshalText([]byte(targetPK)); err != nil {
		return nil, fmt.Errorf("invalid target PK: %w", err)
	}

	entries, err := tps.GetTransports(ctx, target)
	if err != nil {
		return nil, err
	}

	result := make([]tpviz.TPSTransportResponse, len(entries))
	for i, e := range entries {
		result[i] = tpviz.TPSTransportResponse{
			ID:     e.ID.String(),
			Local:  e.Local.String(),
			Remote: e.Remote.String(),
			Type:   string(e.Type),
		}
	}
	return result, nil
}

// PK implements tpviz.TPSAPI.
func (a *tpsAPIAdapter) PK() string {
	tps := a.v.embeddedTPS
	if tps == nil {
		return ""
	}
	return tps.pk.String()
}

// cxoSubMgrAdapter wires the visor's on-demand CXO subscription
// manager into tpviz's narrow CXOSubMgr interface. Tpviz uses int
// for tab/feed identifiers to keep its API decoupled from
// pkg/visor (which would close an import cycle). The values match
// CXOTab / CXOFeed by construction; the cast is safe.
type cxoSubMgrAdapter struct{ v *Visor }

// AcquireForTab forwards to the visor's lazily-constructed manager.
// A nil manager (visor has no DMSG client yet) is a no-op — the
// caller's CXO-first path naturally falls through to its HTTP
// fallback on a subsequent Walk that returns ok=false.
func (a *cxoSubMgrAdapter) AcquireForTab(tab int) {
	a.v.CXOSubMgr().AcquireFor(CXOTab(tab))
}

// ReleaseForTab forwards to the manager. No-op on nil.
func (a *cxoSubMgrAdapter) ReleaseForTab(tab int) {
	a.v.CXOSubMgr().ReleaseFor(CXOTab(tab))
}

// Walk forwards to the manager.
func (a *cxoSubMgrAdapter) Walk(feed int, prefix string, fn func(path string, body []byte) bool) bool {
	return a.v.CXOSubMgr().Walk(CXOFeed(feed), prefix, fn)
}

func initUIServer(ctx context.Context, v *Visor, log *logging.Logger) error {
	// Check if UI server is configured and enabled
	if v.conf.UIServer == nil || !v.conf.UIServer.Enable {
		log.Debug("UI server not configured or disabled, skipping")
		return nil
	}

	conf := v.conf.UIServer

	// Set default local address if not specified
	localAddr := conf.LocalAddr
	if localAddr == "" {
		localAddr = "localhost:8081"
	}

	// Create tpviz config
	tpvizCfg := tpviz.DefaultConfig()
	// Don't set Addr/Port since we're managing the HTTP server ourselves
	tpvizCfg.SurveyDir = conf.SurveyDir

	// Create tpviz server
	tpvizServer := tpviz.NewServer(tpvizCfg)

	// Set visor API directly (bypasses RPC connection) using an adapter
	adapter := &visorAPIAdapter{v: v}
	tpvizServer.SetVisorAPI(adapter, v.conf.PK.Hex())

	// Set TPS API if embedded TPS is running
	if v.embeddedTPS != nil {
		tpvizServer.SetTPSAPI(&tpsAPIAdapter{v: v})
		log.WithField("tps_pk", v.embeddedTPS.pk.Hex()).Info("TPS API connected to UI server")
	}

	// Start cache refresh (without starting HTTP server)
	tpvizServer.Start()

	// Start local HTTP server
	localListener, err := net.Listen("tcp", localAddr)
	if err != nil {
		return fmt.Errorf("failed to start UI server on %s: %w", localAddr, err)
	}

	srv := &http.Server{
		Handler:           tpvizServer.Handler(),
		ReadTimeout:       skyenv.HTTPReadTimeout,
		WriteTimeout:      skyenv.HTTPWriteTimeout,
		IdleTimeout:       skyenv.HTTPIdleTimeout,
		ReadHeaderTimeout: skyenv.HTTPReadHeaderTimeout,
	}

	wg := new(sync.WaitGroup)
	wg.Add(1)

	go func() {
		defer wg.Done()
		log.Infof("UI server listening on http://%s", localAddr)
		if err := srv.Serve(localListener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.WithError(err).Error("UI server exited with error")
		}
	}()

	// Start DMSG listener if configured
	var dmsgListener net.Listener
	if conf.DmsgPort > 0 && v.dmsgC != nil {
		dmsgListener, err = v.dmsgC.Listen(conf.DmsgPort)
		if err != nil {
			log.WithError(err).Warnf("Failed to start UI server on DMSG port %d", conf.DmsgPort)
		} else {
			// Create whitelist middleware if whitelist is configured
			handler := tpvizServer.Handler()
			if len(conf.DmsgWhitelist) > 0 {
				whitelistMap := make(map[cipher.PubKey]struct{}, len(conf.DmsgWhitelist))
				for _, pk := range conf.DmsgWhitelist {
					whitelistMap[pk] = struct{}{}
				}
				handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					// Extract remote public key from dmsg connection
					remoteAddr := r.RemoteAddr
					// DMSG addresses are in format "pk:port"
					if idx := strings.Index(remoteAddr, ":"); idx > 0 {
						pkStr := remoteAddr[:idx]
						var remotePK cipher.PubKey
						if err := remotePK.UnmarshalText([]byte(pkStr)); err == nil {
							if _, allowed := whitelistMap[remotePK]; !allowed {
								log.Warnf("UI server DMSG access denied for %s", pkStr)
								http.Error(w, "Forbidden", http.StatusForbidden)
								return
							}
						}
					}
					tpvizServer.Handler().ServeHTTP(w, r)
				})
			}

			dmsgSrv := &http.Server{
				Handler:           handler,
				ReadTimeout:       skyenv.HTTPReadTimeout,
				WriteTimeout:      skyenv.HTTPWriteTimeout,
				IdleTimeout:       skyenv.HTTPIdleTimeout,
				ReadHeaderTimeout: skyenv.HTTPReadHeaderTimeout,
			}

			wg.Add(1)
			go func() {
				defer wg.Done()
				log.Infof("UI server listening on DMSG port %d", conf.DmsgPort)
				if err := dmsgSrv.Serve(dmsgListener); err != nil && !errors.Is(err, http.ErrServerClosed) && !errors.Is(err, dmsg.ErrEntityClosed) {
					log.WithError(err).Error("UI server DMSG listener exited with error")
				}
			}()

			// Skynet mirror of the UI server at the same port.
			// Whitelist parsing works as-is for skynet conns too:
			// both dmsg.Addr and appnet.Addr stringify as "pk:port"
			// (see appnet.Addr.String), so the existing r.RemoteAddr
			// split by ':' yields the same PK extraction.
			goServeSkynetMirror(ctx, v.conf.PK, conf.DmsgPort, "ui_server", log,
				func(skyLis net.Listener) {
					log.Infof("UI server listening on skynet port %d", conf.DmsgPort)
					if err := dmsgSrv.Serve(skyLis); err != nil &&
						!errors.Is(err, http.ErrServerClosed) &&
						!errors.Is(err, dmsg.ErrEntityClosed) &&
						!errors.Is(err, net.ErrClosed) {
						log.WithError(err).Debug("UI server skynet listener exited")
					}
				})

			v.pushCloseStack("ui_server.dmsg", func() error {
				shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				return dmsgSrv.Shutdown(shutdownCtx)
			})
		}
	}

	v.pushCloseStack("ui_server", func() error {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			log.WithError(err).Warn("UI server shutdown error")
		}
		tpvizServer.Stop()
		wg.Wait()
		return nil
	})

	return nil
}
