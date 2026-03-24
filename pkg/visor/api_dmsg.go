// api_dmsg.go contains DMSG ping, bandwidth, and HTTP API methods.
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
	"time"

	dmsgdisc "github.com/skycoin/dmsg/pkg/disc"
	"github.com/skycoin/dmsg/pkg/dmsg"

	"github.com/skycoin/skywire/pkg/skyenv"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cipher"
)

// DialDmsgPing implements API. Dials a remote visor over dmsg for ping.
// It prefers to use the lowest-latency DMSG server that both visors share.
func (v *Visor) DialDmsgPing(pk cipher.PubKey) error {
	if v.dmsgC == nil {
		return fmt.Errorf("dmsg client not available")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Wait for dmsg client to be ready
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-v.dmsgC.Ready():
	}

	// Try to use the preferred (lowest-latency) server
	preferredServer, err := v.GetPreferredDmsgServer(pk)
	if err == nil && !preferredServer.Null() {
		// Use the preferred server
		v.log.WithField("server", preferredServer.String()[:16]+"...").
			WithField("remote", pk.String()[:16]+"...").
			Debug("Dialing DMSG ping via preferred server")
		return v.DialDmsgPingViaServer(pk, preferredServer)
	}

	// Fall back to default dial (dmsg client will pick a server)
	v.log.WithField("remote", pk.String()[:16]+"...").
		Debug("Dialing DMSG ping via default server selection")

	conn, err := v.dmsgC.Dial(ctx, dmsg.Addr{PK: pk, Port: skyenv.DmsgPingPort})
	if err != nil {
		return fmt.Errorf("failed to dial dmsg ping: %w", err)
	}

	// Extract server PK from the dmsg stream
	var serverPK cipher.PubKey
	if stream, ok := conn.(*dmsg.Stream); ok {
		serverPK = stream.ServerPK()
	}

	v.dmsgPingMx.Lock()
	v.dmsgPingConns[pk] = ping{conn: conn, serverPK: serverPK}
	v.dmsgPingMx.Unlock()

	return nil
}

// DialDmsgPingViaServer implements API. Dials a remote visor over dmsg via a specific server.
func (v *Visor) DialDmsgPingViaServer(pk cipher.PubKey, serverPK cipher.PubKey) error {
	if v.dmsgC == nil {
		return fmt.Errorf("dmsg client not available")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Wait for dmsg client to be ready
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-v.dmsgC.Ready():
	}

	// First ensure we have a session with the specified server
	_, err := v.dmsgC.EnsureAndObtainSession(ctx, serverPK)
	if err != nil {
		return fmt.Errorf("failed to connect to dmsg server %s: %w", serverPK, err)
	}

	// Get the session and dial through it
	session, ok := v.dmsgC.Session(serverPK)
	if !ok {
		return fmt.Errorf("no session with dmsg server %s", serverPK)
	}

	stream, err := session.DialStream(dmsg.Addr{PK: pk, Port: skyenv.DmsgPingPort})
	if err != nil {
		return fmt.Errorf("failed to dial dmsg ping via server %s: %w", serverPK, err)
	}

	v.dmsgPingMx.Lock()
	v.dmsgPingConns[pk] = ping{conn: stream, serverPK: serverPK}
	v.dmsgPingMx.Unlock()

	return nil
}

// DialDmsgRPC implements API. Dials a remote visor's gRPC/RPC port over DMSG.
// Returns a net.Conn that can be used to create a gRPC client.
func (v *Visor) DialDmsgRPC(pk cipher.PubKey) (net.Conn, error) {
	if v.dmsgC == nil {
		return nil, fmt.Errorf("dmsg client not available")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Wait for dmsg client to be ready
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-v.dmsgC.Ready():
	}

	v.log.WithField("remote", pk.String()[:16]+"...").
		Debug("Dialing remote visor RPC over DMSG")

	// Dial to the hypervisor/RPC port
	conn, err := v.dmsgC.Dial(ctx, dmsg.Addr{PK: pk, Port: skyenv.DmsgHypervisorPort})
	if err != nil {
		return nil, fmt.Errorf("failed to dial dmsg RPC: %w", err)
	}

	return conn, nil
}

// GetDmsgPingServerPK implements API. Returns the DMSG server PK used for a ping connection.
func (v *Visor) GetDmsgPingServerPK(pk cipher.PubKey) (cipher.PubKey, error) {
	v.dmsgPingMx.Lock()
	defer v.dmsgPingMx.Unlock()

	pingEntry, ok := v.dmsgPingConns[pk]
	if !ok {
		return cipher.PubKey{}, fmt.Errorf("no dmsg ping connection for %s", pk)
	}

	return pingEntry.serverPK, nil
}

// GetPreferredDmsgServer returns the lowest-latency DMSG server that both the local
// and remote visor are connected to. Returns empty PK if no common server found.
func (v *Visor) GetPreferredDmsgServer(remotePK cipher.PubKey) (cipher.PubKey, error) {
	// Get servers the remote visor is connected to
	remoteServers, err := v.GetRemoteDmsgServers(remotePK)
	if err != nil {
		return cipher.PubKey{}, err
	}
	if len(remoteServers) == 0 {
		return cipher.PubKey{}, fmt.Errorf("remote visor not connected to any DMSG servers")
	}

	// Get our connected servers with latencies
	ourServers, err := v.DMSGServers()
	if err != nil {
		return cipher.PubKey{}, err
	}
	if len(ourServers) == 0 {
		return cipher.PubKey{}, fmt.Errorf("not connected to any DMSG servers")
	}

	// Build set of remote servers for O(1) lookup
	remoteServerSet := make(map[cipher.PubKey]bool)
	for _, s := range remoteServers {
		remoteServerSet[s] = true
	}

	// Find the lowest-latency server we share with the remote visor
	// ourServers is already sorted by latency (lowest first)
	for _, server := range ourServers {
		if remoteServerSet[server.PK] {
			v.log.WithField("server", server.PK.String()[:16]+"...").
				WithField("latency", server.Latency).
				Debug("Selected preferred DMSG server")
			return server.PK, nil
		}
	}

	return cipher.PubKey{}, fmt.Errorf("no common DMSG servers with remote visor")
}

// GetRemoteDmsgServers implements API. Gets the DMSG servers a remote visor is connected to.
func (v *Visor) GetRemoteDmsgServers(pk cipher.PubKey) ([]cipher.PubKey, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Create a dmsg discovery client to query the discovery
	httpC := &http.Client{Timeout: 10 * time.Second}
	discClient := dmsgdisc.NewHTTP(v.conf.Dmsg.Discovery, httpC, v.log)

	entry, err := discClient.Entry(ctx, pk)
	if err != nil {
		return nil, fmt.Errorf("failed to get discovery entry for %s: %w", pk, err)
	}

	if entry.Client == nil {
		return nil, fmt.Errorf("no client entry for %s", pk)
	}

	return entry.Client.DelegatedServers, nil
}

// DmsgPing implements API. Measures round-trip time over dmsg connection.
func (v *Visor) DmsgPing(conf PingConfig) ([]time.Duration, error) {
	v.dmsgPingMx.Lock()
	defer v.dmsgPingMx.Unlock()

	pingEntry, ok := v.dmsgPingConns[conf.PK]
	if !ok {
		return nil, fmt.Errorf("no dmsg ping connection for %s, call DialDmsgPing first", conf.PK)
	}

	return doPingRoundTrips(pingEntry.conn, conf)
}

// DmsgPingViaServer implements API. Performs a ping through a specific DMSG server.
// This is a convenience method that handles dial, ping, and cleanup.
func (v *Visor) DmsgPingViaServer(conf PingConfig, serverPK cipher.PubKey) ([]time.Duration, error) {
	// Dial the ping connection via the specific server
	if err := v.DialDmsgPingViaServer(conf.PK, serverPK); err != nil {
		return nil, fmt.Errorf("dial via server %s: %w", serverPK.String()[:16]+"...", err)
	}

	// Perform the ping
	latencies, err := v.DmsgPing(conf)

	// Always clean up the connection
	if stopErr := v.StopDmsgPing(conf.PK); stopErr != nil {
		v.log.WithError(stopErr).Debug("Failed to stop dmsg ping connection")
	}

	if err != nil {
		return nil, fmt.Errorf("ping via server %s: %w", serverPK.String()[:16]+"...", err)
	}

	return latencies, nil
}

// DmsgPingOnce implements API. Performs a single ping over dmsg connection.
func (v *Visor) DmsgPingOnce(conf PingConfig) (time.Duration, error) {
	v.dmsgPingMx.Lock()
	defer v.dmsgPingMx.Unlock()

	pingEntry, ok := v.dmsgPingConns[conf.PK]
	if !ok {
		return 0, fmt.Errorf("no dmsg ping connection for %s, call DialDmsgPing first", conf.PK)
	}

	data := make([]byte, conf.PcktSize*1024)
	conn := pingEntry.conn
	msg := PingMsg{
		Timestamp: time.Now(),
		PingPk:    conf.PK,
		Data:      data,
	}
	pingData, err := json.Marshal(msg)
	if err != nil {
		return 0, err
	}
	pingSizeMsg := PingSizeMsg{
		Size: len(pingData),
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
	if _, err = conn.Write(pingData); err != nil {
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

// DmsgPingOnceWithEcho performs a single dmsg ping with optional full echo.
// Returns bytes sent, bytes received, latency, and error.
// If echoFull is true, server echoes full payload (for bandwidth testing).
func (v *Visor) DmsgPingOnceWithEcho(conf PingConfig, echoFull bool) (bytesSent, bytesReceived uint64, latency time.Duration, err error) {
	v.dmsgPingMx.Lock()
	defer v.dmsgPingMx.Unlock()

	dmsgEntry, ok := v.dmsgPingConns[conf.PK]
	if !ok {
		return 0, 0, 0, fmt.Errorf("no dmsg ping connection for %s, call DialDmsgPing first", conf.PK)
	}

	data := make([]byte, conf.PcktSize*1024)
	conn := dmsgEntry.conn
	msg := PingMsg{
		Timestamp: time.Now(),
		PingPk:    conf.PK,
		Data:      data,
	}
	pingData, err := json.Marshal(msg)
	if err != nil {
		return 0, 0, 0, err
	}
	pingSizeMsg := PingSizeMsg{
		Size:     len(pingData),
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
	if _, err = conn.Write(pingData); err != nil {
		conn.SetReadDeadline(time.Time{}) //nolint:errcheck,gosec
		return bytesSent, bytesReceived, 0, fmt.Errorf("write ping: %w", err)
	}
	bytesSent += uint64(len(pingData))

	// Read echo response with timeout
	conn.SetReadDeadline(time.Now().Add(30 * time.Second)) //nolint:errcheck,gosec
	if echoFull {
		// Read full payload echo
		var received int
		for received < len(pingData) {
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

// StopDmsgPing implements API.
func (v *Visor) StopDmsgPing(pk cipher.PubKey) error {
	v.dmsgPingMx.Lock()
	defer v.dmsgPingMx.Unlock()

	dmsgConn, ok := v.dmsgPingConns[pk]
	if !ok {
		return fmt.Errorf("no dmsg ping connection for %s", pk)
	}
	err := dmsgConn.conn.Close()
	if err != nil {
		return err
	}
	delete(v.dmsgPingConns, pk)
	return nil
}

// DmsgBandwidthTest implements API.
// Performs a bandwidth test over dmsg by sending and receiving data for the specified duration.
func (v *Visor) DmsgBandwidthTest(conf BandwidthTestConfig) (BandwidthResult, error) {
	// First establish a dmsg ping connection if not already connected
	v.dmsgPingMx.Lock()
	_, exists := v.dmsgPingConns[conf.PK]
	v.dmsgPingMx.Unlock()

	if !exists {
		if err := v.DialDmsgPing(conf.PK); err != nil {
			return BandwidthResult{}, fmt.Errorf("failed to dial dmsg: %w", err)
		}
	}

	v.dmsgPingMx.Lock()
	dmsgEntry, ok := v.dmsgPingConns[conf.PK]
	if !ok {
		v.dmsgPingMx.Unlock()
		return BandwidthResult{}, fmt.Errorf("no dmsg ping connection for %s", conf.PK)
	}
	conn := dmsgEntry.conn
	v.dmsgPingMx.Unlock()

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

// IsDMSGClientReady return availability of dsmg client
func (v *Visor) IsDMSGClientReady() (bool, error) {
	if v.isDTMReady() {
		dmsgTracker, ok := v.dtm.Get(v.conf.PK)
		if ok && dmsgTracker.ServerPK.Hex()[:5] != "00000" {
			return true, nil
		}
	}
	return false, errors.New("dmsg client is not ready")
}

// DMSGServers returns list of connected DMSG servers with their latencies.
// Servers are sorted by latency (lowest first). Servers with latency 0 have not been measured yet.
func (v *Visor) DMSGServers() ([]DMSGServerInfo, error) {
	if v.dmsgC == nil {
		return nil, errors.New("dmsg client not available")
	}

	// Get connected servers
	serverPKs := v.dmsgC.ConnectedServersPK()
	if len(serverPKs) == 0 {
		return []DMSGServerInfo{}, nil
	}

	// Build list with latencies
	servers := make([]DMSGServerInfo, 0, len(serverPKs))
	v.dmsgServerLatenciesMu.RLock()
	for _, pkStr := range serverPKs {
		var pk cipher.PubKey
		if err := pk.Set(pkStr); err != nil {
			continue
		}
		info := DMSGServerInfo{
			PK:      pk,
			Latency: v.dmsgServerLatencies[pk],
		}
		servers = append(servers, info)
	}
	v.dmsgServerLatenciesMu.RUnlock()

	// Sort by latency (lowest first, 0/unmeasured at end)
	for i := 0; i < len(servers)-1; i++ {
		for j := i + 1; j < len(servers); j++ {
			si, sj := servers[i], servers[j]
			// 0 latency (unmeasured) goes to the end
			if si.Latency == 0 && sj.Latency > 0 {
				servers[i], servers[j] = servers[j], servers[i]
			} else if si.Latency > 0 && sj.Latency > 0 && sj.Latency < si.Latency {
				servers[i], servers[j] = servers[j], servers[i]
			}
		}
	}

	return servers, nil
}

// DmsgHTTP implements API. Performs an HTTP request over dmsg using the visor's dmsg client.
func (v *Visor) DmsgHTTP(req DmsgHTTPRequest) (*DmsgHTTPResponse, error) {
	if v.dmsgC == nil {
		return nil, fmt.Errorf("dmsg client not available")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// Wait for dmsg client to be ready
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-v.dmsgC.Ready():
	}

	// Create HTTP transport using visor's dmsg client
	transport := dmsgHTTPTransport{
		ctx:   ctx,
		dmsgC: v.dmsgC,
	}

	httpClient := &http.Client{
		Transport: &transport,
		Timeout:   55 * time.Second,
	}

	// Build HTTP request
	var bodyReader io.Reader
	if len(req.Body) > 0 {
		bodyReader = bytes.NewReader(req.Body)
	}

	httpReq, err := http.NewRequestWithContext(ctx, req.Method, req.URL, bodyReader)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	// Set headers
	for k, v := range req.Header {
		httpReq.Header.Set(k, v)
	}

	// Perform request
	resp, err := httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck

	// Read response body
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	// Build response
	response := &DmsgHTTPResponse{
		StatusCode: resp.StatusCode,
		Status:     resp.Status,
		Header:     make(map[string]string),
		Body:       body,
	}

	for k, v := range resp.Header {
		if len(v) > 0 {
			response.Header[k] = v[0]
		}
	}

	return response, nil
}

// dmsgHTTPTransport implements http.RoundTripper using the visor's dmsg client
type dmsgHTTPTransport struct {
	ctx   context.Context
	dmsgC *dmsg.Client
}

func (t *dmsgHTTPTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	var hostAddr dmsg.Addr
	if err := hostAddr.Set(req.Host); err != nil {
		return nil, fmt.Errorf("invalid host address: %w", err)
	}
	if hostAddr.Port == 0 {
		hostAddr.Port = 80
	}

	stream, err := t.dmsgC.DialStream(req.Context(), hostAddr)
	if err != nil {
		return nil, err
	}

	if err = req.Write(stream); err != nil {
		_ = stream.Close() //nolint:errcheck
		return nil, err
	}

	resp, err := http.ReadResponse(bufio.NewReader(stream), req)
	if err != nil {
		_ = stream.Close() //nolint:errcheck
		return nil, err
	}

	// Wrap response body to close stream when done
	resp.Body = &dmsgStreamBody{
		ReadCloser: resp.Body,
		stream:     stream,
	}

	return resp, nil
}

type dmsgStreamBody struct {
	io.ReadCloser
	stream *dmsg.Stream
}

func (b *dmsgStreamBody) Close() error {
	err1 := b.ReadCloser.Close()
	err2 := b.stream.Close()
	if err1 != nil {
		return err1
	}
	return err2
}
