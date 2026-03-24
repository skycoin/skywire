// init_services.go contains service initialization logic: health tracking, uptime, survey,
// forwarding connections, ping listeners, and the UI server.
package visor

import (
	"bufio"
	"bytes"
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
	"github.com/skycoin/dmsg/pkg/dmsg"
	"github.com/skycoin/dmsg/pkg/dmsgcurl"

	"github.com/skycoin/skywire/pkg/app/appevent"
	"github.com/skycoin/skywire/pkg/app/appnet"
	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/skyenv"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/logging"
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
	go GenerateSurvey(v, log, true)
	return nil
}

func initUptimeTracker(ctx context.Context, v *Visor, log *logging.Logger) error {
	const tickDuration = 5 * time.Minute

	conf := v.conf.UptimeTracker

	if conf == nil {
		v.log.Debug("'uptime_tracker' is not configured, skipping.")
		return nil
	}

	httpC, err := getHTTPClient(ctx, v, conf.Addr)
	if err != nil {
		return err
	}

	pIP, err := getPublicIP(v, conf.Addr)
	if err != nil {
		return err
	}

	ut, err := utclient.NewHTTP(conf.Addr, v.conf.PK, v.conf.SK, httpC, pIP, v.MasterLogger())
	if err != nil {
		v.log.WithError(err).Warn("Failed to connect to uptime tracker.")
		return nil
	}

	ticker := time.NewTicker(tickDuration)

	go func() {
		for range ticker.C {
			c := context.Background()
			if err := ut.UpdateVisorUptime(c, v.conf.Version); err != nil {
				v.isServicesHealthy.unset()
				log.WithError(err).Warn("Failed to update visor uptime.")
			} else {
				v.isServicesHealthy.set()
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

	pIP, err = GetIP(v.conf.GeoIP)
	if err != nil {
		<-v.stunReady
		if v.stunClient.PublicIP != nil {
			pIP = v.stunClient.PublicIP.IP()
			return pIP, nil
		}
		err = fmt.Errorf("cannot fetch public ip")
	}
	if err != nil {
		return pIP, err
	}

	return pIP, nil
}

// GeoData holds geolocation information from the geoIP service.
type GeoData struct {
	CountryCode string  `json:"country_code,omitempty"`
	CountryName string  `json:"country_name,omitempty"`
	RegionCode  string  `json:"region_code,omitempty"`
	RegionName  string  `json:"region_name,omitempty"`
	CityName    string  `json:"city_name,omitempty"`
	Latitude    float64 `json:"latitude,omitempty"`
	Longitude   float64 `json:"longitude,omitempty"`
}

type geoIPResponse struct {
	IP          string   `json:"ip_address"`
	CountryCode string   `json:"country_code"`
	CountryName string   `json:"country_name"`
	RegionCode  string   `json:"region_code"`
	RegionName  string   `json:"region_name"`
	CityName    string   `json:"city_name"`
	Latitude    *float64 `json:"latitude"`
	Longitude   *float64 `json:"longitude"`
}

// GetIP used for getting current IP of visor
func GetIP(geoipURL string) (string, error) {
	ip, _ := GetIPWithGeo(geoipURL)
	return ip, nil
}

// GetIPWithGeo fetches public IP and geolocation data from the geoIP service.
func GetIPWithGeo(geoipURL string) (string, *GeoData) {
	client := &http.Client{
		Timeout: 10 * time.Second,
	}

	resp, err := client.Get(geoipURL)
	if err != nil {
		return "", nil
	}
	defer resp.Body.Close() //nolint:errcheck

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", nil
	}

	var geoResp geoIPResponse
	err = json.Unmarshal(body, &geoResp)
	if err != nil {
		return "", nil
	}

	geo := &GeoData{
		CountryCode: geoResp.CountryCode,
		CountryName: geoResp.CountryName,
		RegionCode:  geoResp.RegionCode,
		RegionName:  geoResp.RegionName,
		CityName:    geoResp.CityName,
	}
	if geoResp.Latitude != nil {
		geo.Latitude = *geoResp.Latitude
	}
	if geoResp.Longitude != nil {
		geo.Longitude = *geoResp.Longitude
	}

	return geoResp.IP, geo
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
			go handleServerConn(log, wrappedConn, v)
		}
	}()

	return nil
}

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
	log.Debugf("Received: %v", cMsg)

	lHost := fmt.Sprintf("localhost:%v", cMsg.Port)
	ok := isPortRegistered(cMsg.Port, v)
	if !ok {
		log.Errorf("Port :%v not registered", cMsg.Port)
		sendError(log, remoteConn, fmt.Errorf("port :%v not registered", cMsg.Port))
		return
	}

	ok = isPortAvailable(log, cMsg.Port)
	if ok {
		log.Errorf("Failed to dial port %v", cMsg.Port)
		sendError(log, remoteConn, fmt.Errorf("failed to dial port %v", cMsg.Port))
		return
	}

	log.Debugf("Forwarding %s (raw_tcp=%v)", lHost, cMsg.RawTCP)

	// send nil error to indicate to the remote connection that everything is ok
	sendError(log, remoteConn, nil)

	go forward(log, remoteConn, lHost, cMsg.RawTCP)
}

// forward proxies data between remoteConn (the skywire connection) and a local server.
// When rawTCP is true, it uses bidirectional io.Copy for raw TCP proxying.
// When rawTCP is false, it reads HTTP requests and forwards them to the local server.
func forward(log *logging.Logger, remoteConn net.Conn, lHost string, rawTCP bool) {
	if rawTCP {
		forwardRawTCP(log, remoteConn, lHost)
		return
	}
	forwardHTTP(log, remoteConn, lHost)
}

// forwardRawTCP does bidirectional raw TCP proxying using io.Copy
func forwardRawTCP(log *logging.Logger, remoteConn net.Conn, lHost string) {
	localConn, err := net.Dial("tcp", lHost)
	if err != nil {
		log.WithError(err).Error("Failed to dial local server")
		closeConn(log, remoteConn)
		return
	}

	done := make(chan struct{}, 2)

	// remote -> local
	go func() {
		_, err := io.Copy(localConn, remoteConn)
		if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, net.ErrClosed) {
			log.WithError(err).Debug("remote->local copy ended")
		}
		done <- struct{}{}
	}()

	// local -> remote
	go func() {
		_, err := io.Copy(remoteConn, localConn)
		if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, net.ErrClosed) {
			log.WithError(err).Debug("local->remote copy ended")
		}
		done <- struct{}{}
	}()

	// Wait for one direction to finish, then close both
	<-done
	closeConn(log, remoteConn)
	closeConn(log, localConn)
	<-done
}

// forwardHTTP reads HTTP requests from remoteConn and forwards them to the local server
func forwardHTTP(log *logging.Logger, remoteConn net.Conn, lHost string) {
	for {
		buf := make([]byte, 32*1024)
		n, err := remoteConn.Read(buf)
		if err != nil {
			log.WithError(err).Error("Failed to read packet")
			closeConn(log, remoteConn)
			return
		}
		req, err := http.ReadRequest(bufio.NewReader(bytes.NewBuffer(buf[:n])))
		if err != nil {
			log.WithError(err).Error("Failed to ReadRequest")
			closeConn(log, remoteConn)
			return
		}
		req.RequestURI = ""
		req.URL.Scheme = "http"
		req.URL.Host = lHost
		client := http.Client{}
		resp, err := client.Do(req)
		if err != nil {
			log.WithError(err).Error("Failed to Do req")
			closeConn(log, remoteConn)
			return
		}
		err = resp.Write(remoteConn)
		if err != nil {
			log.WithError(err).Error("Failed to Write")
			closeConn(log, remoteConn)
			return
		}
	}
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
	Port   int  `json:"port"`
	RawTCP bool `json:"raw_tcp,omitempty"`
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
