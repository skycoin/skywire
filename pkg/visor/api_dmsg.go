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
	"sort"
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
	dmsgdisc "github.com/skycoin/skywire/pkg/dmsg/disc"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/skyenv"
)

// DialDmsgPing implements API. Dials a remote visor over dmsg for ping.
// It prefers to use the lowest-latency DMSG server that both visors share.
func (v *Visor) DialDmsgPing(pk cipher.PubKey) error {
	if err := v.mustWaitDmsgReady(); err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Try to use the preferred (lowest-latency) server
	preferredServer, err := v.GetPreferredDmsgServer(pk)
	if err == nil && !preferredServer.Null() {
		// Use the preferred server
		v.log.WithField("server", preferredServer.String()).
			WithField("remote", pk.String()).
			Debug("Dialing DMSG ping via preferred server")
		return v.DialDmsgPingViaServer(pk, preferredServer)
	}

	// Fall back to default dial (dmsg client will pick a server)
	v.log.WithField("remote", pk.String()).
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

	v.dmsgPing.mu.Lock()
	v.dmsgPing.conns[pk] = ping{conn: conn, serverPK: serverPK}
	v.dmsgPing.mu.Unlock()

	return nil
}

// DialDmsgPingViaServer implements API. Dials a remote visor over dmsg via a specific server.
func (v *Visor) DialDmsgPingViaServer(pk cipher.PubKey, serverPK cipher.PubKey) error {
	if err := v.mustWaitDmsgReady(); err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

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

	stream, err := session.DialStream(context.Background(), dmsg.Addr{PK: pk, Port: skyenv.DmsgPingPort})
	if err != nil {
		return fmt.Errorf("failed to dial dmsg ping via server %s: %w", serverPK, err)
	}

	v.dmsgPing.mu.Lock()
	v.dmsgPing.conns[pk] = ping{conn: stream, serverPK: serverPK}
	v.dmsgPing.mu.Unlock()

	return nil
}

// DialDmsgRPC implements API. Dials a remote visor's gRPC/RPC port over DMSG.
// Returns a net.Conn that can be used to create a gRPC client.
func (v *Visor) DialDmsgRPC(pk cipher.PubKey) (net.Conn, error) {
	if err := v.mustWaitDmsgReady(); err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	v.log.WithField("remote", pk.String()).
		Debug("Dialing remote visor RPC over DMSG")

	// Dial to the gRPC DMSG port on the remote visor
	conn, err := v.dmsgC.Dial(ctx, dmsg.Addr{PK: pk, Port: skyenv.DmsgGRPCPort})
	if err != nil {
		return nil, fmt.Errorf("failed to dial dmsg RPC: %w", err)
	}

	return conn, nil
}

// GetDmsgPingServerPK implements API. Returns the DMSG server PK used for a ping connection.
func (v *Visor) GetDmsgPingServerPK(pk cipher.PubKey) (cipher.PubKey, error) {
	v.dmsgPing.mu.Lock()
	defer v.dmsgPing.mu.Unlock()

	pingEntry, ok := v.dmsgPing.conns[pk]
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
			v.log.WithField("server", server.PK.String()).
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
//
// Mutex scope: same pattern as the skynet-side Ping — hold
// dmsgPing.mu only for the map lookup, release before wire I/O.
// Lets concurrent DmsgPing calls on different PKs proceed in
// parallel instead of serializing through one global lock.
func (v *Visor) DmsgPing(conf PingConfig) ([]time.Duration, error) {
	v.dmsgPing.mu.Lock()
	pingEntry, ok := v.dmsgPing.conns[conf.PK]
	v.dmsgPing.mu.Unlock()
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
		return nil, fmt.Errorf("dial via server %s: %w", serverPK.String(), err)
	}

	// Perform the ping
	latencies, err := v.DmsgPing(conf)

	// Always clean up the connection
	if stopErr := v.StopDmsgPing(conf.PK); stopErr != nil {
		v.log.WithError(stopErr).Debug("Failed to stop dmsg ping connection")
	}

	if err != nil {
		return nil, fmt.Errorf("ping via server %s: %w", serverPK.String(), err)
	}

	return latencies, nil
}

// DmsgPingOnce implements API. Performs a single ping over dmsg connection.
//
// See DmsgPing for mutex-scoping rationale.
func (v *Visor) DmsgPingOnce(conf PingConfig) (time.Duration, error) {
	v.dmsgPing.mu.Lock()
	pingEntry, ok := v.dmsgPing.conns[conf.PK]
	v.dmsgPing.mu.Unlock()
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
//
// See DmsgPing for mutex-scoping rationale.
func (v *Visor) DmsgPingOnceWithEcho(conf PingConfig, echoFull bool) (bytesSent, bytesReceived uint64, latency time.Duration, err error) {
	v.dmsgPing.mu.Lock()
	dmsgEntry, ok := v.dmsgPing.conns[conf.PK]
	v.dmsgPing.mu.Unlock()
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
	v.dmsgPing.mu.Lock()
	defer v.dmsgPing.mu.Unlock()

	dmsgConn, ok := v.dmsgPing.conns[pk]
	if !ok {
		return fmt.Errorf("no dmsg ping connection for %s", pk)
	}
	err := dmsgConn.conn.Close()
	if err != nil {
		return err
	}
	delete(v.dmsgPing.conns, pk)
	return nil
}

// DmsgBandwidthTest implements API.
// Performs a bandwidth test over dmsg by sending and receiving data for the specified duration.
func (v *Visor) DmsgBandwidthTest(conf BandwidthTestConfig) (BandwidthResult, error) {
	// First establish a dmsg ping connection if not already connected
	v.dmsgPing.mu.Lock()
	_, exists := v.dmsgPing.conns[conf.PK]
	v.dmsgPing.mu.Unlock()

	if !exists {
		if err := v.DialDmsgPing(conf.PK); err != nil {
			return BandwidthResult{}, fmt.Errorf("failed to dial dmsg: %w", err)
		}
	}

	v.dmsgPing.mu.Lock()
	dmsgEntry, ok := v.dmsgPing.conns[conf.PK]
	if !ok {
		v.dmsgPing.mu.Unlock()
		return BandwidthResult{}, fmt.Errorf("no dmsg ping connection for %s", conf.PK)
	}
	conn := dmsgEntry.conn
	v.dmsgPing.mu.Unlock()

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
		dmsgTracker, ok := v.dmsgTracker.manager.Get(v.conf.PK)
		if ok && dmsgTracker.ServerPK.Hex() != "00000" {
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
	v.dmsgLatency.mu.RLock()
	for _, pkStr := range serverPKs {
		var pk cipher.PubKey
		if err := pk.Set(pkStr); err != nil {
			continue
		}
		info := DMSGServerInfo{
			PK:      pk,
			Latency: v.dmsgLatency.servers[pk],
		}
		servers = append(servers, info)
	}
	v.dmsgLatency.mu.RUnlock()

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

// DmsgConnectAllResult summarizes the result of DmsgConnectAll.
type DmsgConnectAllResult struct {
	Total            int               `json:"total"`             // total servers in discovery
	AlreadyConnected int               `json:"already_connected"` // sessions in place before the call
	NewlyConnected   int               `json:"newly_connected"`   // sessions opened by the call
	Failed           map[string]string `json:"failed,omitempty"`  // server PK → error text for any that could not be connected
}

// DmsgConnectAll enumerates every dmsg server in discovery and ensures the
// visor's dmsg client has an active session to each one. This is a one-shot
// action intended for RSN / TPS visors that need to reach arbitrary
// destinations without relying on phase-3 new-session dials during route
// setup. It does not mutate the visor's configured sessions_count — for
// persistent behavior use SetDmsgSessionsCount with 0 (connect to all)
// or edit sessions_count in the visor config.
func (v *Visor) DmsgConnectAll() (*DmsgConnectAllResult, error) {
	if err := v.mustWaitDmsgReady(); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	res, err := v.dmsgC.ConnectToAllServers(ctx)
	if err != nil {
		return nil, err
	}
	out := &DmsgConnectAllResult{
		Total:            res.Total,
		AlreadyConnected: res.AlreadyConnected,
		NewlyConnected:   res.NewlyConnected,
	}
	if len(res.Failed) > 0 {
		out.Failed = make(map[string]string, len(res.Failed))
		for pk, errStr := range res.Failed {
			out.Failed[pk.String()] = errStr
		}
	}
	return out, nil
}

// SetDmsgSessionsCount updates the visor's persisted dmsg.sessions_count
// setting (written to the config file so it survives restart) and
// immediately attempts to maximize current session count if the new value
// is higher or zero (zero = connect to all available servers).
//
// Note: the live dmsg.Client's MinSessions is not currently mutable at
// runtime — the change takes full effect on next visor restart. The
// DmsgConnectAll one-shot is invoked inline so the visor reaches the
// desired connectivity immediately in addition to persisting the change.
func (v *Visor) SetDmsgSessionsCount(count int) (*DmsgConnectAllResult, error) {
	if count < 0 {
		return nil, errors.New("sessions_count must be >= 0")
	}
	if v.conf == nil || v.conf.Dmsg == nil {
		return nil, errors.New("dmsg config not initialized")
	}
	v.conf.Dmsg.SessionsCount = count
	if err := v.conf.Flush(); err != nil {
		return nil, fmt.Errorf("failed to persist config: %w", err)
	}
	v.log.WithField("sessions_count", count).Info("Updated dmsg.sessions_count in config")

	// Trigger the one-shot so the running visor reaches the new target now
	// without needing a restart.
	return v.DmsgConnectAll()
}

// DmsgSessions enumerates all dmsg clients running inside the visor and
// returns the server PKs each one has an active session with. Covers the
// main visor dmsg client, the embedded route setup node's dmsg client
// (if configured), and the embedded transport setup node's dmsg client
// (if configured). Each of these runs under a DIFFERENT identity/key and
// maintains its OWN session set, so checking `visor info` alone does not
// tell you whether the RSN or TPS is actually reaching the network.
func (v *Visor) DmsgSessions() (*DmsgClientSessions, error) {
	out := &DmsgClientSessions{}

	if v.dmsgC != nil {
		servers := dmsgClientServerPKs(v.dmsgC)
		out.Main = &DmsgClientSessionInfo{
			PK:      v.conf.PK,
			Role:    "main",
			Count:   len(servers),
			Servers: servers,
		}
	}

	v.initLock.Lock()
	rsn := v.embeddedRouteSetup
	tps := v.embeddedTPS
	v.initLock.Unlock()

	if rsn != nil && rsn.DmsgClient() != nil {
		servers := dmsgClientServerPKs(rsn.DmsgClient())
		out.RouteSetup = &DmsgClientSessionInfo{
			PK:      rsn.PK(),
			Role:    "route_setup",
			Count:   len(servers),
			Servers: servers,
		}
	}
	if tps != nil && tps.DmsgClient() != nil {
		servers := dmsgClientServerPKs(tps.DmsgClient())
		out.TransportSetup = &DmsgClientSessionInfo{
			PK:      tps.PK(),
			Role:    "transport_setup",
			Count:   len(servers),
			Servers: servers,
		}
	}
	return out, nil
}

// dmsgClientServerPKs returns the PKs of the dmsg servers the given client
// currently has an active session with. Sorted by PK for stable output.
func dmsgClientServerPKs(c *dmsg.Client) []cipher.PubKey {
	strs := c.ConnectedServersPK()
	out := make([]cipher.PubKey, 0, len(strs))
	for _, s := range strs {
		var pk cipher.PubKey
		if err := pk.Set(s); err == nil {
			out = append(out, pk)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].String() < out[j].String() })
	return out
}

// DmsgProbe checks whether a remote PK is reachable on a given dmsg port
// by performing a DialStream + immediate Close. The noise handshake completing
// confirms end-to-end reachability. Returns true if reachable.
func (v *Visor) DmsgProbe(pk cipher.PubKey, port uint16) (bool, error) {
	if err := v.mustWaitDmsgReady(); err != nil {
		return false, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	return v.dmsgC.Probe(ctx, pk, port), nil
}

// DmsgHTTP implements API. Performs an HTTP request over dmsg using the visor's dmsg client.
func (v *Visor) DmsgHTTP(req DmsgHTTPRequest) (*DmsgHTTPResponse, error) {
	// Use the visor's main DMSG client (v.dmsgC) for HTTP-over-DMSG.
	// Deployment services are registered in the DMSG discovery, so
	// DialStream resolves them via normal lookup + delegated-server phases.
	//
	// Note: v.dmsgHTTP uses a SEPARATE dmsg.Client (dmsgDC) sharing the
	// same PK, which causes session conflicts on DMSG servers. v.dmsgC
	// is the authoritative client with stable sessions.
	if err := v.mustWaitDmsgReady(); err != nil {
		return nil, fmt.Errorf("DMSG client not ready: %w", err)
	}

	httpClient := &http.Client{
		Transport: &dmsgHTTPTransport{ctx: context.Background(), dmsgC: v.dmsgC},
		Timeout:   15 * time.Second,
	}

	// Build HTTP request
	var bodyReader io.Reader
	if len(req.Body) > 0 {
		bodyReader = bytes.NewReader(req.Body)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

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

	// Dial the service. DialStream does discovery lookup → connected
	// server fallback. For deployment services (which have server entries,
	// not client entries), the discovery lookup fails and the fallback
	// kicks in. The fallback tries all connected DMSG servers as forwarders.
	// This is the correct path — the service IS connected to one of our
	// servers, and the server forwards the stream.
	//
	// We use a shorter timeout here since the CLI is waiting interactively.
	dialCtx, dialCancel := context.WithTimeout(req.Context(), 10*time.Second)
	defer dialCancel()

	stream, err := t.dmsgC.DialStream(dialCtx, hostAddr)
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
