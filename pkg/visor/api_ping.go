// api_ping.go contains ping, bandwidth test, and visor test API methods.
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
	"time"

	"golang.org/x/net/proxy"

	"github.com/skycoin/skywire/deployment"
	"github.com/skycoin/skywire/pkg/app/appnet"
	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/netutil"
	"github.com/skycoin/skywire/pkg/router"
	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/skyenv"
)

// DialPing implements API.
func (v *Visor) DialPing(conf PingConfig) error {
	v.ping.pcktSize = conf.PcktSize
	// waiting for at least one transport to initialize
	<-v.tpM.Ready()

	// Set local route calculation if requested
	if conf.LocalRoute {
		if err := v.SetForceLocalRoutes(true); err != nil {
			v.log.WithError(err).Warn("Failed to enable local route calculation")
		} else {
			defer func() {
				if err := v.SetForceLocalRoutes(false); err != nil {
					v.log.WithError(err).Warn("Failed to disable local route calculation")
				}
			}()
		}
	}

	addr := appnet.Addr{
		Net:    appnet.TypeSkynet,
		PubKey: conf.PK,
		Port:   routing.Port(skyenv.SkyPingPort),
	}

	var err error
	var conn net.Conn

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var r = netutil.NewRetrier(v.log, 2*time.Second, netutil.DefaultMaxBackoff, 1, 2)
	err = r.Do(ctx, func() error {
		switch {
		case len(conf.ForwardHops) > 0 && len(conf.ReverseHops) > 0:
			// Use explicit route (skips route calculation)
			fwdHops := make([]appnet.RouteHopInfo, len(conf.ForwardHops))
			for i, h := range conf.ForwardHops {
				fwdHops[i] = appnet.RouteHopInfo{TpID: h.TpID, From: h.From, To: h.To, TpType: h.TpType}
			}
			revHops := make([]appnet.RouteHopInfo, len(conf.ReverseHops))
			for i, h := range conf.ReverseHops {
				revHops[i] = appnet.RouteHopInfo{TpID: h.TpID, From: h.From, To: h.To, TpType: h.TpType}
			}
			conn, err = appnet.PingContextWithRoute(ctx, conf.PK, addr, fwdHops, revHops)
		case conf.TransportID != "":
			// Use specific transport (skips route calculation)
			conn, err = appnet.PingContextWithTransport(ctx, conf.PK, addr, conf.TransportID)
		case conf.MinHops > 0:
			// Constrain the route-finder to multi-hop paths only.
			// This is what `mux-bw --min-hops N` rides — without
			// this branch the router would short-circuit to the
			// direct transport whenever one existed, making the
			// flag a no-op.
			conn, err = appnet.PingContextWithMinHops(ctx, conf.PK, addr, conf.MinHops)
		default:
			conn, err = appnet.PingContext(ctx, conf.PK, addr)
		}
		return err
	})
	if err != nil {
		return err
	}

	// Extract route hops from the connection if available
	var hops []cipher.PubKey
	var hopInfos []router.RouteHopInfo
	if skyConn, ok := conn.(*appnet.SkywireConn); ok {
		hops = skyConn.RouteHops()
		hopInfos = skyConn.RouteHopDetails()
	}

	v.ping.mu.Lock()
	v.ping.conns[PingRouteRef{PK: conf.PK, Index: conf.RouteIndex}] = ping{
		conn:     conn,
		hops:     hops,
		hopInfos: hopInfos,
	}
	v.ping.mu.Unlock()
	return nil
}

// Ping implements API.
// Measures round-trip time by sending ping data and waiting for an echo response.
func (v *Visor) Ping(conf PingConfig) ([]time.Duration, error) {
	v.ping.mu.Lock()
	defer v.ping.mu.Unlock()

	pingEntry, ok := v.ping.conns[PingRouteRef{PK: conf.PK, Index: conf.RouteIndex}]
	if !ok {
		return nil, fmt.Errorf("no ping connection for %s#%d, call DialPing first", conf.PK, conf.RouteIndex)
	}

	return doPingRoundTrips(pingEntry.conn, conf)
}

// doPingRoundTrips performs the ping protocol: send size, read ack, send data, read echo.
// Shared by Ping() and DmsgPing() which differ only in connection lookup.
func doPingRoundTrips(conn net.Conn, conf PingConfig) ([]time.Duration, error) {
	latencies := []time.Duration{}
	data := make([]byte, conf.PcktSize*1024)

	for i := 1; i <= conf.Tries; i++ {
		msg := PingMsg{
			Timestamp: time.Now(),
			PingPk:    conf.PK,
			Data:      data,
		}
		pingData, err := json.Marshal(msg)
		if err != nil {
			return latencies, err
		}
		sizeMsg := PingSizeMsg{Size: len(pingData)}
		size, err := json.Marshal(sizeMsg)
		if err != nil {
			return latencies, err
		}

		start := time.Now()

		if _, err = conn.Write(size); err != nil {
			return latencies, fmt.Errorf("write size: %w", err)
		}

		conn.SetReadDeadline(time.Now().Add(30 * time.Second)) //nolint:errcheck,gosec
		buf := make([]byte, 32*1024)
		if _, err = conn.Read(buf); err != nil {
			conn.SetReadDeadline(time.Time{}) //nolint:errcheck,gosec
			return latencies, fmt.Errorf("read ack: %w", err)
		}

		if _, err = conn.Write(pingData); err != nil {
			conn.SetReadDeadline(time.Time{}) //nolint:errcheck,gosec
			return latencies, fmt.Errorf("write ping: %w", err)
		}

		conn.SetReadDeadline(time.Now().Add(30 * time.Second)) //nolint:errcheck,gosec
		if _, err = conn.Read(buf); err != nil {
			conn.SetReadDeadline(time.Time{}) //nolint:errcheck,gosec
			return latencies, fmt.Errorf("read echo: %w", err)
		}
		conn.SetReadDeadline(time.Time{}) //nolint:errcheck,gosec

		latencies = append(latencies, time.Since(start))
	}
	return latencies, nil
}

// PingOnce implements API.
// Performs a single ping and returns the round-trip time.
func (v *Visor) PingOnce(conf PingConfig) (time.Duration, error) {
	v.ping.mu.Lock()
	defer v.ping.mu.Unlock()

	pingEntry, ok := v.ping.conns[PingRouteRef{PK: conf.PK, Index: conf.RouteIndex}]
	if !ok {
		return 0, fmt.Errorf("no ping connection for %s#%d, call DialPing first", conf.PK, conf.RouteIndex)
	}

	data := make([]byte, conf.PcktSize*1024)
	conn := pingEntry.conn
	msg := PingMsg{
		Timestamp: time.Now(),
		PingPk:    conf.PK,
		Data:      data,
	}
	ping, err := json.Marshal(msg)
	if err != nil {
		return 0, err
	}
	pingSizeMsg := PingSizeMsg{
		Size: len(ping),
	}
	size, err := json.Marshal(pingSizeMsg)
	if err != nil {
		return 0, err
	}

	start := time.Now()

	// Send size message
	if _, err = conn.Write(size); err != nil {
		return 0, fmt.Errorf("write size: %w", err)
	}

	// Read "ok" ack with timeout
	conn.SetReadDeadline(time.Now().Add(30 * time.Second)) //nolint:errcheck,gosec
	buf := make([]byte, 32*1024)
	if _, err = conn.Read(buf); err != nil {
		conn.SetReadDeadline(time.Time{}) //nolint:errcheck,gosec
		return 0, fmt.Errorf("read ack: %w", err)
	}

	// Send ping data
	if _, err = conn.Write(ping); err != nil {
		conn.SetReadDeadline(time.Time{}) //nolint:errcheck,gosec
		return 0, fmt.Errorf("write ping: %w", err)
	}

	// Read echo response with timeout
	conn.SetReadDeadline(time.Now().Add(30 * time.Second)) //nolint:errcheck,gosec
	if _, err = conn.Read(buf); err != nil {
		conn.SetReadDeadline(time.Time{}) //nolint:errcheck,gosec
		return 0, fmt.Errorf("read echo: %w", err)
	}
	conn.SetReadDeadline(time.Time{}) //nolint:errcheck,gosec

	return time.Since(start), nil
}

// PingOnceWithEcho performs a single ping with optional full echo.
// Returns bytes sent, bytes received, latency, and error.
// If echoFull is true, server echoes full payload (for bandwidth testing).
func (v *Visor) PingOnceWithEcho(conf PingConfig, echoFull bool) (bytesSent, bytesReceived uint64, latency time.Duration, err error) {
	v.ping.mu.Lock()
	defer v.ping.mu.Unlock()

	pingEntry, ok := v.ping.conns[PingRouteRef{PK: conf.PK, Index: conf.RouteIndex}]
	if !ok {
		return 0, 0, 0, fmt.Errorf("no ping connection for %s#%d, call DialPing first", conf.PK, conf.RouteIndex)
	}

	data := make([]byte, conf.PcktSize*1024)
	conn := pingEntry.conn
	msg := PingMsg{
		Timestamp: time.Now(),
		PingPk:    conf.PK,
		Data:      data,
	}
	ping, err := json.Marshal(msg)
	if err != nil {
		return 0, 0, 0, err
	}
	pingSizeMsg := PingSizeMsg{
		Size:     len(ping),
		EchoFull: echoFull,
	}
	size, err := json.Marshal(pingSizeMsg)
	if err != nil {
		return 0, 0, 0, err
	}

	start := time.Now()

	// Send size message
	if _, err = conn.Write(size); err != nil {
		return 0, 0, 0, fmt.Errorf("write size: %w", err)
	}
	bytesSent += uint64(len(size))

	// Read "ok" ack with timeout
	conn.SetReadDeadline(time.Now().Add(30 * time.Second)) //nolint:errcheck,gosec
	buf := make([]byte, 32*1024)
	n, err := conn.Read(buf)
	if err != nil {
		conn.SetReadDeadline(time.Time{}) //nolint:errcheck,gosec
		return bytesSent, bytesReceived, 0, fmt.Errorf("read ack: %w", err)
	}
	bytesReceived += uint64(n) //nolint:gosec

	// Send ping data
	if _, err = conn.Write(ping); err != nil {
		conn.SetReadDeadline(time.Time{}) //nolint:errcheck,gosec
		return bytesSent, bytesReceived, 0, fmt.Errorf("write ping: %w", err)
	}
	bytesSent += uint64(len(ping))

	// Read echo response with timeout
	conn.SetReadDeadline(time.Now().Add(30 * time.Second)) //nolint:errcheck,gosec
	if echoFull {
		// Read full payload echo
		var received int
		for received < len(ping) {
			n, err = conn.Read(buf)
			if err != nil {
				conn.SetReadDeadline(time.Time{}) //nolint:errcheck,gosec
				return bytesSent, bytesReceived, 0, fmt.Errorf("read echo: %w", err)
			}
			received += n
			bytesReceived += uint64(n) //nolint:gosec
		}
	} else {
		// Read simple "pong" response
		n, err = conn.Read(buf)
		if err != nil {
			conn.SetReadDeadline(time.Time{}) //nolint:errcheck,gosec
			return bytesSent, bytesReceived, 0, fmt.Errorf("read echo: %w", err)
		}
		bytesReceived += uint64(n) //nolint:gosec
	}
	conn.SetReadDeadline(time.Time{}) //nolint:errcheck,gosec

	return bytesSent, bytesReceived, time.Since(start), nil
}

// StopPing implements API.
//
// Tears down ALL routes to the given peer (every PingRouteRef whose
// PK matches). Callers that want to tear down a single route in a
// mux-set must use StopPingRoute(ref) instead. The legacy semantics
// (single PK = single connection) are preserved for the common
// case where no aux routes exist.
func (v *Visor) StopPing(pk cipher.PubKey) error {
	v.ping.mu.Lock()
	defer v.ping.mu.Unlock()

	var firstErr error
	for ref, entry := range v.ping.conns {
		if ref.PK != pk {
			continue
		}
		if entry.conn != nil {
			if err := entry.conn.Close(); err != nil && firstErr == nil {
				firstErr = err
			}
		}
		delete(v.ping.conns, ref)
	}
	return firstErr
}

// StopPingRoute closes a single route in a mux-set without
// touching the other parallel routes to the same peer. Used by
// `cli visor ping mux-bw` when one of N parallel routes fails
// mid-pump and we want to keep the others alive, and by future
// mux-aware proxies that want to drop one leg of a multi-route
// session.
//
// Returns nil if the ref doesn't match any known conn (idempotent —
// safe to call from cleanup paths that aren't sure whether the route
// was ever established).
func (v *Visor) StopPingRoute(ref PingRouteRef) error {
	v.ping.mu.Lock()
	defer v.ping.mu.Unlock()

	entry, ok := v.ping.conns[ref]
	if !ok || entry.conn == nil {
		delete(v.ping.conns, ref)
		return nil
	}
	err := entry.conn.Close()
	delete(v.ping.conns, ref)
	return err
}

// StopAllPings stops all active ping connections and cleans up their routes.
// Returns the number of connections stopped, error messages, and any fatal error.
func (v *Visor) StopAllPings() (int, []string, error) {
	v.ping.mu.Lock()
	defer v.ping.mu.Unlock()

	var errs []string
	count := 0

	for ref, pingEntry := range v.ping.conns {
		if pingEntry.conn != nil {
			if err := pingEntry.conn.Close(); err != nil {
				errs = append(errs, fmt.Sprintf("failed to close ping to %s: %v", ref, err))
			}
		}
		delete(v.ping.conns, ref)
		count++
	}

	return count, errs, nil
}

// GetPingRoute returns the route hops for the primary ping
// connection to a peer (RouteIndex 0). For aux routes in a mux-set
// use GetPingRouteAt(ref).
func (v *Visor) GetPingRoute(pk cipher.PubKey) []cipher.PubKey {
	return v.GetPingRouteAt(PingRoutePrimary(pk))
}

// GetPingRouteAt returns the route hops for a specific ping route.
func (v *Visor) GetPingRouteAt(ref PingRouteRef) []cipher.PubKey {
	v.ping.mu.Lock()
	defer v.ping.mu.Unlock()

	if pingEntry, ok := v.ping.conns[ref]; ok {
		return pingEntry.hops
	}
	return nil
}

// GetPingRouteDetails returns detailed route information for the
// PRIMARY ping connection to a peer (RouteIndex 0), including
// transport IDs and types for each hop. For aux routes use
// GetPingRouteDetailsAt(ref).
func (v *Visor) GetPingRouteDetails(pk cipher.PubKey) []router.RouteHopInfo {
	return v.GetPingRouteDetailsAt(PingRoutePrimary(pk))
}

// GetPingRouteDetailsAt returns detailed route information for a
// specific ping route. Used by mux-aware callers (mux-bw) to surface
// the hops of each parallel route — the primary-keyed accessor
// can't distinguish among them.
func (v *Visor) GetPingRouteDetailsAt(ref PingRouteRef) []router.RouteHopInfo {
	v.ping.mu.Lock()
	defer v.ping.mu.Unlock()

	if pingEntry, ok := v.ping.conns[ref]; ok {
		return pingEntry.hopInfos
	}
	return nil
}

// GetLastRouteCalcTime returns the time spent calculating the last local route.
func (v *Visor) GetLastRouteCalcTime() time.Duration {
	if v.router == nil {
		return 0
	}
	return v.router.GetLastRouteCalcTime()
}

// BandwidthTest implements API.
// Performs a bandwidth test over a skywire route by sending and receiving data for the specified duration.
func (v *Visor) BandwidthTest(conf BandwidthTestConfig) (BandwidthResult, error) {
	// First establish a ping connection if not already connected
	pingConf := PingConfig{
		PK:         conf.PK,
		Tries:      1,
		PcktSize:   conf.PacketSize,
		LocalRoute: conf.LocalRoute,
	}

	// BandwidthTest is a single-route caller; use the primary slot.
	primary := PingRoutePrimary(conf.PK)

	v.ping.mu.Lock()
	_, exists := v.ping.conns[primary]
	v.ping.mu.Unlock()

	if !exists {
		if err := v.DialPing(pingConf); err != nil {
			return BandwidthResult{}, fmt.Errorf("failed to dial: %w", err)
		}
	}

	v.ping.mu.Lock()
	pingEntry, ok := v.ping.conns[primary]
	if !ok {
		v.ping.mu.Unlock()
		return BandwidthResult{}, fmt.Errorf("no ping connection for %s", conf.PK)
	}
	conn := pingEntry.conn
	v.ping.mu.Unlock()

	// Prepare packet with EchoFull flag for download measurement
	packetSize := conf.PacketSize
	if packetSize <= 0 {
		packetSize = 32 // Default 32KB packets for bandwidth test
	}
	data := make([]byte, packetSize*1024)

	var bytesSent, bytesReceived uint64
	start := time.Now()
	deadline := start.Add(conf.Duration)

	for time.Now().Before(deadline) {
		msg := PingMsg{
			Timestamp: time.Now(),
			PingPk:    conf.PK,
			Data:      data,
		}
		ping, err := json.Marshal(msg)
		if err != nil {
			return BandwidthResult{}, err
		}

		// Request full echo for download measurement
		pingSizeMsg := PingSizeMsg{
			Size:     len(ping),
			EchoFull: true,
		}
		size, err := json.Marshal(pingSizeMsg)
		if err != nil {
			return BandwidthResult{}, err
		}

		// Send size message
		if _, err = conn.Write(size); err != nil {
			return BandwidthResult{}, fmt.Errorf("write size: %w", err)
		}
		bytesSent += uint64(len(size))

		// Read "ok" ack
		conn.SetReadDeadline(time.Now().Add(30 * time.Second)) //nolint:errcheck,gosec
		buf := make([]byte, 32*1024)
		n, err := conn.Read(buf)
		if err != nil {
			conn.SetReadDeadline(time.Time{}) //nolint:errcheck,gosec
			return BandwidthResult{}, fmt.Errorf("read ack: %w", err)
		}
		bytesReceived += uint64(n) //nolint:gosec

		// Send ping data
		if _, err = conn.Write(ping); err != nil {
			conn.SetReadDeadline(time.Time{}) //nolint:errcheck,gosec
			return BandwidthResult{}, fmt.Errorf("write ping: %w", err)
		}
		bytesSent += uint64(len(ping))

		// Read full echo response
		conn.SetReadDeadline(time.Now().Add(30 * time.Second)) //nolint:errcheck,gosec
		var received int
		for received < len(ping) {
			n, err = conn.Read(buf)
			if err != nil {
				conn.SetReadDeadline(time.Time{}) //nolint:errcheck,gosec
				return BandwidthResult{}, fmt.Errorf("read echo: %w", err)
			}
			received += n
			bytesReceived += uint64(n) //nolint:gosec
		}
		conn.SetReadDeadline(time.Time{}) //nolint:errcheck,gosec
	}

	duration := time.Since(start)
	durationSec := duration.Seconds()

	return BandwidthResult{
		BytesSent:     bytesSent,
		BytesReceived: bytesReceived,
		Duration:      duration,
		UploadSpeed:   float64(bytesSent) / 1024 / durationSec,
		DownloadSpeed: float64(bytesReceived) / 1024 / durationSec,
	}, nil
}

// TestVisor trying to test visor
func (v *Visor) TestVisor(conf PingConfig) ([]TestResult, error) {
	result := []TestResult{}
	if v.dmsgC == nil {
		return result, errors.New("dmsgC is not available")
	}

	publicVisors, err := v.dmsgC.AllEntries(context.TODO())
	if err != nil {
		return result, err
	}

	if conf.PubVisCount+1 <= len(publicVisors) {
		publicVisors = publicVisors[:conf.PubVisCount+1]
	}

	for _, publicVisor := range publicVisors {
		if publicVisor == v.conf.PK.Hex() {
			continue
		}

		if err := conf.PK.UnmarshalText([]byte(publicVisor)); err != nil {
			continue
		}
		err := v.DialPing(conf)
		if err != nil {
			result = append(result, TestResult{PK: conf.PK.String(), Max: fmt.Sprint(0), Min: fmt.Sprint(0), Mean: fmt.Sprint(0), Status: "Failed"})
			continue
		}
		latencies, err := v.Ping(conf)
		if err != nil {
			go v.StopPing(conf.PK) //nolint:errcheck,gosec
			result = append(result, TestResult{PK: conf.PK.String(), Max: fmt.Sprint(0), Min: fmt.Sprint(0), Mean: fmt.Sprint(0), Status: "Failed"})
			continue
		}
		var maxx, minn, mean, sumLatency time.Duration
		minn = time.Duration(10000000000)
		for _, latency := range latencies {
			if latency > maxx {
				maxx = latency
			}
			if latency < minn {
				minn = latency
			}
			sumLatency += latency
		}
		mean = sumLatency / time.Duration(len(latencies))
		result = append(result, TestResult{PK: conf.PK.String(), Max: fmt.Sprint(maxx), Min: fmt.Sprint(minn), Mean: fmt.Sprint(mean), Status: "Success"})
		v.StopPing(conf.PK) //nolint:errcheck,gosec
	}
	return result, nil
}

// TestProxy tests proxy servers by connecting through them and fetching a test URL
func (v *Visor) TestProxy(conf ProxyTestConfig) ([]ProxyTestResult, error) {
	results := make([]ProxyTestResult, 0, len(conf.Servers))

	// Set defaults
	if conf.TestURL == "" {
		conf.TestURL = deployment.Prod.GeoIP
	}
	if conf.Timeout == 0 {
		conf.Timeout = 30 * time.Second
	}

	// Get the skysocks-client address from config
	socksAddr := skyenv.SkysocksClientAddr

	for _, serverPK := range conf.Servers {
		result := ProxyTestResult{
			PK: serverPK.String(),
		}

		start := time.Now()

		// Start skysocks-client with this server
		err := v.StartSkysocksClient(serverPK.String())
		if err != nil {
			result.Status = "FAIL"
			result.Error = fmt.Sprintf("failed to start client: %v", err)
			result.Duration = time.Since(start).Milliseconds()
			results = append(results, result)
			continue
		}

		// Give it a moment to establish connection
		time.Sleep(500 * time.Millisecond)

		// Create SOCKS5 dialer
		dialer, err := proxy.SOCKS5("tcp", socksAddr, nil, proxy.Direct)
		if err != nil {
			v.StopSkysocksClients() //nolint:errcheck,gosec
			result.Status = "FAIL"
			result.Error = fmt.Sprintf("failed to create SOCKS dialer: %v", err)
			result.Duration = time.Since(start).Milliseconds()
			results = append(results, result)
			continue
		}

		// Create HTTP client with SOCKS transport
		httpTransport := &http.Transport{
			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				return dialer.Dial(network, addr)
			},
		}
		httpClient := &http.Client{
			Transport: httpTransport,
			Timeout:   conf.Timeout,
		}

		// Make test request
		ctx, cancel := context.WithTimeout(context.Background(), conf.Timeout)
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, conf.TestURL, nil)
		if err != nil {
			cancel()
			v.StopSkysocksClients() //nolint:errcheck,gosec
			result.Status = "FAIL"
			result.Error = fmt.Sprintf("failed to create request: %v", err)
			result.Duration = time.Since(start).Milliseconds()
			results = append(results, result)
			continue
		}

		resp, err := httpClient.Do(req)
		cancel()
		if err != nil {
			v.StopSkysocksClients() //nolint:errcheck,gosec
			if errors.Is(err, context.DeadlineExceeded) {
				result.Status = "TIMEOUT"
			} else {
				result.Status = "FAIL"
			}
			result.Error = err.Error()
			result.Duration = time.Since(start).Milliseconds()
			results = append(results, result)
			continue
		}

		// Read response body
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close() //nolint:errcheck,gosec
		if err != nil {
			v.StopSkysocksClients() //nolint:errcheck,gosec
			result.Status = "FAIL"
			result.Error = fmt.Sprintf("failed to read response: %v", err)
			result.Duration = time.Since(start).Milliseconds()
			results = append(results, result)
			continue
		}

		// Parse response - try to extract IP and location
		// Expected format from ip.skycoin.com: {"ip":"1.2.3.4","country":"US","city":"New York",...}
		var ipData struct {
			IP      string `json:"ip"`
			Country string `json:"country"`
			City    string `json:"city"`
		}
		if err := json.Unmarshal(body, &ipData); err == nil {
			result.IP = ipData.IP
			if ipData.City != "" && ipData.Country != "" {
				result.Location = fmt.Sprintf("%s,%s", ipData.City, ipData.Country)
			} else if ipData.Country != "" {
				result.Location = ipData.Country
			}
		} else {
			// If JSON parsing fails, try to use the body as plain text IP
			result.IP = strings.TrimSpace(string(body))
		}

		result.Status = "OK"
		result.Duration = time.Since(start).Milliseconds()

		// Stop the client before next iteration
		v.StopSkysocksClients() //nolint:errcheck,gosec

		results = append(results, result)
	}

	return results, nil
}
