// Package rpcgrpc provides gRPC streaming services for the visor
package rpcgrpc

import (
	"context"
	"fmt"
	"io"
	"net"
	"time"

	"github.com/google/uuid"
	"github.com/sirupsen/logrus"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/logging"
	routeFinder "github.com/skycoin/skywire/pkg/route-finder/store"
	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/transport"
	tpdstore "github.com/skycoin/skywire/pkg/transport-discovery/store"
	tptypes "github.com/skycoin/skywire/pkg/transport/types"
)

// RouteHopInfo contains detailed information about a single hop in a route.
// Mirrors router.RouteHopInfo to avoid import cycles.
type RouteHopInfo struct {
	TpID   string
	From   string
	To     string
	TpType string
}

// VisorAPI defines the interface for visor ping operations
type VisorAPI interface {
	DialPing(conf PingConf) error
	PingOnce(conf PingConf) (time.Duration, error)
	PingOnceWithEcho(conf PingConf, echoFull bool) (bytesSent, bytesReceived uint64, latency time.Duration, err error)
	StopPing(pk cipher.PubKey) error
	GetPingRoute(pk cipher.PubKey) []cipher.PubKey
	GetPingRouteDetails(pk cipher.PubKey) []RouteHopInfo
	GetLastRouteCalcTime() time.Duration
	DialDmsgPing(pk cipher.PubKey) error
	DialDmsgPingViaServer(pk cipher.PubKey, serverPK cipher.PubKey) error
	DmsgPingOnce(conf PingConf) (time.Duration, error)
	DmsgPingOnceWithEcho(conf PingConf, echoFull bool) (bytesSent, bytesReceived uint64, latency time.Duration, err error)
	StopDmsgPing(pk cipher.PubKey) error
	GetDmsgPingServerPK(pk cipher.PubKey) (cipher.PubKey, error)
	GetRemoteDmsgServers(pk cipher.PubKey) ([]cipher.PubKey, error)
	// DialDmsgRPC dials a remote visor's gRPC/RPC port over DMSG and returns the connection
	DialDmsgRPC(pk cipher.PubKey) (net.Conn, error)
	// SubscribeLogs returns a channel of logrus entries matching the
	// filter; cancel() unsubscribes and returns the dropped count.
	// Both may be nil (visor without a broadcaster wired up).
	SubscribeLogs(f logging.Filter, capacity int) (<-chan *logrus.Entry, func() uint64)
	// SubscribeGroupMessages returns a channel of inbound group
	// messages converted into GroupMessageData (rpcgrpc's wire-friendly
	// mirror of visor.GroupMessage to avoid the visor → rpcgrpc import
	// cycle). cancel() unsubscribes and returns the dropped count.
	// Returns (nil, no-op) when grouping is not yet initialized on
	// the visor or running in a test harness.
	SubscribeGroupMessages(capacity int) (<-chan GroupMessageData, func() uint64)
	// SnapshotGroupMessagesAfterNs returns inbox-buffered group
	// messages with TS > sinceNs (UnixNano). Used by
	// StreamGroupMessages to replay events that landed during a
	// client's reconnect gap before entering the live dispatch loop.
	// Empty slice when grouping is uninitialized; bounded by the
	// inbox ring capacity (long disconnect → only the tail is
	// recoverable).
	SnapshotGroupMessagesAfterNs(sinceNs int64) []GroupMessageData
	// SnapshotGroupHistoryAfterNs returns durably-persisted group
	// messages with TS > sinceNs for the named group. Powers the
	// history-fallback path that backfills a subscriber whose
	// reconnect gap exceeds the in-memory inbox ring. Empty when the
	// operator hasn't enabled GroupHistoryDB, when the group has no
	// stored messages, or when no messages newer than sinceNs exist.
	SnapshotGroupHistoryAfterNs(groupID string, sinceNs int64) []GroupMessageData
	// RecordGroupStreamSend bumps the visor's inbox-wide successful-
	// gRPC-stream.Send counter. StreamGroupMessages calls this after
	// every successful stream.Send to a CLI subscriber (including the
	// Subscribed sentinel). Surfaced as GroupInfo.StreamSendCount;
	// closes the layer-9 hop in the per-layer counter ladder so an
	// operator can localize a drop to the stream.Send seam vs the
	// upstream inbox→sub.ch fan-out (SubDropCount) or the inbox layer
	// itself (DeliverCount). No-op when grouping is uninitialized.
	RecordGroupStreamSend()
	// LocalPK returns the visor's own public key. Used by route-calc
	// gRPC handlers that default the src PK to the local visor.
	LocalPK() cipher.PubKey
	// FetchAllTransportEntries pulls every transport entry from TPD via
	// the visor's existing discovery client. The route-calc gRPC
	// handler builds the BFS graph from this slice.
	FetchAllTransportEntries(ctx context.Context) ([]*transport.Entry, error)
}

// GroupMessageData mirrors visor.GroupMessage one-for-one but lives in
// rpcgrpc so VisorAPI.SubscribeGroupMessages doesn't pull pkg/visor
// into rpcgrpc's import graph. The adapter at the visor's init wires
// a tiny converter goroutine between the inbox's
// chan visor.GroupMessage and this chan GroupMessageData.
type GroupMessageData struct {
	TimestampNs int64
	GroupID     string
	SenderPK    string
	Body        string
}

// PingConf mirrors visor.PingConfig to avoid import cycles
type PingConf struct {
	PK          cipher.PubKey
	Tries       int
	PcktSize    int
	LocalRoute  bool
	TransportID string         // Optional: use specific transport (skips route calculation)
	ForwardHops []RouteHopInfo // Optional: explicit forward route (skips route calculation)
	ReverseHops []RouteHopInfo // Optional: explicit reverse route (skips route calculation)
}

// PingServer implements the gRPC PingService
type PingServer struct {
	UnimplementedPingServiceServer
	visor VisorAPI
	log   *logging.Logger
}

// NewPingServer creates a new gRPC ping server
func NewPingServer(visor VisorAPI, log *logging.Logger) *PingServer {
	return &PingServer{
		visor: visor,
		log:   log,
	}
}

// StreamPing performs multiple pings and streams results back
func (s *PingServer) StreamPing(req *PingRequest, stream PingService_StreamPingServer) error {
	var pk cipher.PubKey
	if err := pk.Set(req.PublicKey); err != nil {
		return fmt.Errorf("invalid public key: %w", err)
	}

	// Convert proto RouteHop to RouteHopInfo
	var forwardHops, reverseHops []RouteHopInfo
	for _, h := range req.ForwardHops {
		forwardHops = append(forwardHops, RouteHopInfo{
			TpID:   h.TpId,
			From:   h.From,
			To:     h.To,
			TpType: h.TpType,
		})
	}
	for _, h := range req.ReverseHops {
		reverseHops = append(reverseHops, RouteHopInfo{
			TpID:   h.TpId,
			From:   h.From,
			To:     h.To,
			TpType: h.TpType,
		})
	}

	conf := PingConf{
		PK:          pk,
		Tries:       int(req.Tries),
		PcktSize:    int(req.PacketSizeKb),
		LocalRoute:  req.LocalRoute,
		TransportID: req.TransportId,
		ForwardHops: forwardHops,
		ReverseHops: reverseHops,
	}

	// Dial first (with optional setup timeout)
	s.log.Debugf("gRPC StreamPing: dialing %s", pk)
	setupStart := time.Now()
	setupTimeout := time.Duration(req.SetupTimeoutNs)

	var dialErr error
	if setupTimeout > 0 {
		// Use timeout for route setup
		dialDone := make(chan error, 1)
		go func() {
			dialDone <- s.visor.DialPing(conf)
		}()

		select {
		case dialErr = <-dialDone:
			// Dial completed (success or failure)
		case <-time.After(setupTimeout):
			s.log.Warnf("gRPC StreamPing: route setup timeout after %v for %s", setupTimeout, pk)
			return fmt.Errorf("route setup timeout after %v", setupTimeout)
		case <-stream.Context().Done():
			return stream.Context().Err()
		}
	} else {
		dialErr = s.visor.DialPing(conf)
	}

	if dialErr != nil {
		return fmt.Errorf("dial ping failed: %w", dialErr)
	}
	setupTime := time.Since(setupStart)

	// Get route hops for the setup response (legacy)
	var routeHops []string
	if hops := s.visor.GetPingRoute(pk); hops != nil {
		routeHops = make([]string, len(hops))
		for i, hop := range hops {
			routeHops[i] = hop.String()
		}
	}

	// Get detailed route hop info
	var routeHopDetails []*RouteHop
	if details := s.visor.GetPingRouteDetails(pk); details != nil {
		routeHopDetails = make([]*RouteHop, len(details))
		for i, d := range details {
			routeHopDetails[i] = &RouteHop{
				TpId:   d.TpID,
				From:   d.From,
				To:     d.To,
				TpType: d.TpType,
			}
		}
	}

	// Get route calculation time (for local route mode)
	routeCalcTime := s.visor.GetLastRouteCalcTime()

	// Send setup time as first result
	if err := stream.Send(&PingResult{
		Sequence:        0,
		LatencyNs:       setupTime.Nanoseconds(),
		IsSetup:         true,
		RouteHops:       routeHops,
		RouteHopDetails: routeHopDetails,
		RouteCalcTimeNs: routeCalcTime.Nanoseconds(),
	}); err != nil {
		_ = s.visor.StopPing(pk) //nolint:errcheck
		return err
	}

	// Perform pings and stream each result
	pingTimeout := time.Duration(req.PingTimeoutNs)

	for i := 1; i <= conf.Tries; i++ {
		// Check for context cancellation before each ping
		select {
		case <-stream.Context().Done():
			_ = s.visor.StopPing(pk) //nolint:errcheck
			return stream.Context().Err()
		default:
		}

		// Execute ping with optional per-ping timeout
		var latency time.Duration
		var pingErr error

		if pingTimeout > 0 {
			// Run ping with timeout
			type pingResult struct {
				latency time.Duration
				err     error
			}
			resultCh := make(chan pingResult, 1)
			go func() {
				lat, err := s.visor.PingOnce(conf)
				resultCh <- pingResult{lat, err}
			}()

			select {
			case res := <-resultCh:
				latency = res.latency
				pingErr = res.err
			case <-time.After(pingTimeout):
				_ = s.visor.StopPing(pk) //nolint:errcheck
				return fmt.Errorf("ping %d timed out after %v", i, pingTimeout)
			case <-stream.Context().Done():
				_ = s.visor.StopPing(pk) //nolint:errcheck
				return stream.Context().Err()
			}
		} else {
			latency, pingErr = s.visor.PingOnce(conf)
		}

		result := &PingResult{
			Sequence:  int32(i), //nolint:gosec
			LatencyNs: latency.Nanoseconds(),
		}
		if pingErr != nil {
			result.Error = pingErr.Error()
		}

		if err := stream.Send(result); err != nil {
			_ = s.visor.StopPing(pk) //nolint:errcheck
			return err
		}
	}

	// Cleanup
	return s.visor.StopPing(pk)
}

// StreamDmsgPing performs multiple pings over dmsg and streams results
func (s *PingServer) StreamDmsgPing(req *PingRequest, stream PingService_StreamDmsgPingServer) error {
	var pk cipher.PubKey
	if err := pk.Set(req.PublicKey); err != nil {
		return fmt.Errorf("invalid public key: %w", err)
	}

	conf := PingConf{
		PK:       pk,
		Tries:    int(req.Tries),
		PcktSize: int(req.PacketSizeKb),
	}

	// Dial first - optionally via specific server
	s.log.Debugf("gRPC StreamDmsgPing: dialing %s", pk)
	setupStart := time.Now()

	if req.DmsgServerPk != "" {
		// Dial via specific server
		var serverPK cipher.PubKey
		if err := serverPK.Set(req.DmsgServerPk); err != nil {
			return fmt.Errorf("invalid dmsg server public key: %w", err)
		}
		if err := s.visor.DialDmsgPingViaServer(pk, serverPK); err != nil {
			return fmt.Errorf("dial dmsg ping via server %s failed: %w", serverPK, err)
		}
	} else {
		if err := s.visor.DialDmsgPing(pk); err != nil {
			return fmt.Errorf("dial dmsg ping failed: %w", err)
		}
	}
	setupTime := time.Since(setupStart)

	// Get the server PK used for this connection
	serverPK, _ := s.visor.GetDmsgPingServerPK(pk) //nolint:errcheck
	serverPKStr := ""
	if !serverPK.Null() {
		serverPKStr = serverPK.String()
	}

	// Send setup time as first result with server PK
	if err := stream.Send(&PingResult{
		Sequence:     0,
		LatencyNs:    setupTime.Nanoseconds(),
		IsSetup:      true,
		DmsgServerPk: serverPKStr,
	}); err != nil {
		_ = s.visor.StopDmsgPing(pk) //nolint:errcheck
		return err
	}

	// Perform pings and stream each result
	pingTimeout := time.Duration(req.PingTimeoutNs)

	for i := 1; i <= conf.Tries; i++ {
		// Check for context cancellation before each ping
		select {
		case <-stream.Context().Done():
			_ = s.visor.StopDmsgPing(pk) //nolint:errcheck
			return stream.Context().Err()
		default:
		}

		// Execute ping with optional per-ping timeout
		var latency time.Duration
		var pingErr error

		if pingTimeout > 0 {
			// Run ping with timeout
			type pingResult struct {
				latency time.Duration
				err     error
			}
			resultCh := make(chan pingResult, 1)
			go func() {
				lat, err := s.visor.DmsgPingOnce(conf)
				resultCh <- pingResult{lat, err}
			}()

			select {
			case res := <-resultCh:
				latency = res.latency
				pingErr = res.err
			case <-time.After(pingTimeout):
				_ = s.visor.StopDmsgPing(pk) //nolint:errcheck
				return fmt.Errorf("dmsg ping %d timed out after %v", i, pingTimeout)
			case <-stream.Context().Done():
				_ = s.visor.StopDmsgPing(pk) //nolint:errcheck
				return stream.Context().Err()
			}
		} else {
			latency, pingErr = s.visor.DmsgPingOnce(conf)
		}

		result := &PingResult{
			Sequence:  int32(i), //nolint:gosec
			LatencyNs: latency.Nanoseconds(),
		}
		if pingErr != nil {
			result.Error = pingErr.Error()
		}

		if err := stream.Send(result); err != nil {
			_ = s.visor.StopDmsgPing(pk) //nolint:errcheck
			return err
		}
	}

	// Cleanup
	return s.visor.StopDmsgPing(pk)
}

// StreamBandwidthTest performs a bandwidth test over skywire route and streams progress
func (s *PingServer) StreamBandwidthTest(req *BandwidthRequest, stream PingService_StreamBandwidthTestServer) error {
	var pk cipher.PubKey
	if err := pk.Set(req.PublicKey); err != nil {
		return fmt.Errorf("invalid public key: %w", err)
	}

	packetSize := int(req.PacketSizeKb)
	if packetSize <= 0 {
		packetSize = 32 // Default 32KB
	}

	conf := PingConf{
		PK:         pk,
		Tries:      1,
		PcktSize:   packetSize,
		LocalRoute: req.LocalRoute,
	}

	// Dial first
	s.log.Debugf("gRPC StreamBandwidthTest: dialing %s", pk)
	if err := s.visor.DialPing(conf); err != nil {
		return fmt.Errorf("dial ping failed: %w", err)
	}

	duration := time.Duration(req.DurationNs)
	if duration <= 0 {
		duration = 10 * time.Second // Default 10 seconds
	}

	var totalSent, totalReceived uint64
	start := time.Now()
	deadline := start.Add(duration)
	lastUpdate := start

	for time.Now().Before(deadline) {
		select {
		case <-stream.Context().Done():
			_ = s.visor.StopPing(pk) //nolint:errcheck
			return stream.Context().Err()
		default:
		}

		bytesSent, bytesReceived, _, err := s.visor.PingOnceWithEcho(conf, true)
		if err != nil {
			_ = s.visor.StopPing(pk) //nolint:errcheck
			return fmt.Errorf("bandwidth test failed: %w", err)
		}

		totalSent += bytesSent
		totalReceived += bytesReceived

		// Send progress update every 500ms
		if time.Since(lastUpdate) >= 500*time.Millisecond {
			elapsed := time.Since(start)
			elapsedSec := elapsed.Seconds()

			if err := stream.Send(&BandwidthProgress{
				BytesSent:     totalSent,
				BytesReceived: totalReceived,
				ElapsedNs:     elapsed.Nanoseconds(),
				UploadSpeed:   float64(totalSent) / 1024 / elapsedSec,
				DownloadSpeed: float64(totalReceived) / 1024 / elapsedSec,
				IsFinal:       false,
			}); err != nil {
				_ = s.visor.StopPing(pk) //nolint:errcheck
				return err
			}
			lastUpdate = time.Now()
		}
	}

	// Send final result
	elapsed := time.Since(start)
	elapsedSec := elapsed.Seconds()

	if err := stream.Send(&BandwidthProgress{
		BytesSent:     totalSent,
		BytesReceived: totalReceived,
		ElapsedNs:     elapsed.Nanoseconds(),
		UploadSpeed:   float64(totalSent) / 1024 / elapsedSec,
		DownloadSpeed: float64(totalReceived) / 1024 / elapsedSec,
		IsFinal:       true,
	}); err != nil {
		_ = s.visor.StopPing(pk) //nolint:errcheck
		return err
	}

	return s.visor.StopPing(pk)
}

// StreamDmsgBandwidthTest performs a bandwidth test over dmsg and streams progress
func (s *PingServer) StreamDmsgBandwidthTest(req *BandwidthRequest, stream PingService_StreamDmsgBandwidthTestServer) error {
	var pk cipher.PubKey
	if err := pk.Set(req.PublicKey); err != nil {
		return fmt.Errorf("invalid public key: %w", err)
	}

	packetSize := int(req.PacketSizeKb)
	if packetSize <= 0 {
		packetSize = 32 // Default 32KB
	}

	conf := PingConf{
		PK:       pk,
		Tries:    1,
		PcktSize: packetSize,
	}

	// Dial first
	s.log.Debugf("gRPC StreamDmsgBandwidthTest: dialing %s", pk)
	if err := s.visor.DialDmsgPing(pk); err != nil {
		return fmt.Errorf("dial dmsg ping failed: %w", err)
	}

	duration := time.Duration(req.DurationNs)
	if duration <= 0 {
		duration = 10 * time.Second // Default 10 seconds
	}

	var totalSent, totalReceived uint64
	start := time.Now()
	deadline := start.Add(duration)
	lastUpdate := start

	for time.Now().Before(deadline) {
		select {
		case <-stream.Context().Done():
			_ = s.visor.StopDmsgPing(pk) //nolint:errcheck
			return stream.Context().Err()
		default:
		}

		bytesSent, bytesReceived, _, err := s.visor.DmsgPingOnceWithEcho(conf, true)
		if err != nil {
			_ = s.visor.StopDmsgPing(pk) //nolint:errcheck
			return fmt.Errorf("dmsg bandwidth test failed: %w", err)
		}

		totalSent += bytesSent
		totalReceived += bytesReceived

		// Send progress update every 500ms
		if time.Since(lastUpdate) >= 500*time.Millisecond {
			elapsed := time.Since(start)
			elapsedSec := elapsed.Seconds()

			if err := stream.Send(&BandwidthProgress{
				BytesSent:     totalSent,
				BytesReceived: totalReceived,
				ElapsedNs:     elapsed.Nanoseconds(),
				UploadSpeed:   float64(totalSent) / 1024 / elapsedSec,
				DownloadSpeed: float64(totalReceived) / 1024 / elapsedSec,
				IsFinal:       false,
			}); err != nil {
				_ = s.visor.StopDmsgPing(pk) //nolint:errcheck
				return err
			}
			lastUpdate = time.Now()
		}
	}

	// Send final result
	elapsed := time.Since(start)
	elapsedSec := elapsed.Seconds()

	if err := stream.Send(&BandwidthProgress{
		BytesSent:     totalSent,
		BytesReceived: totalReceived,
		ElapsedNs:     elapsed.Nanoseconds(),
		UploadSpeed:   float64(totalSent) / 1024 / elapsedSec,
		DownloadSpeed: float64(totalReceived) / 1024 / elapsedSec,
		IsFinal:       true,
	}); err != nil {
		_ = s.visor.StopDmsgPing(pk) //nolint:errcheck
		return err
	}

	return s.visor.StopDmsgPing(pk)
}

// GetRemoteDmsgServers returns the DMSG servers a remote visor is connected to
func (s *PingServer) GetRemoteDmsgServers(_ context.Context, req *DmsgServersRequest) (*DmsgServersResponse, error) {
	var pk cipher.PubKey
	if err := pk.Set(req.PublicKey); err != nil {
		return nil, fmt.Errorf("invalid public key: %w", err)
	}

	servers, err := s.visor.GetRemoteDmsgServers(pk)
	if err != nil {
		return nil, fmt.Errorf("failed to get remote dmsg servers: %w", err)
	}

	serverPKs := make([]string, len(servers))
	for i, srv := range servers {
		serverPKs[i] = srv.String()
	}

	return &DmsgServersResponse{
		ServerPks: serverPKs,
	}, nil
}

// statsCollector is used for system stats collection
var statsCollector = NewSystemStatsCollector()

// StreamSystemStats streams system stats for gotop-style monitoring
func (s *PingServer) StreamSystemStats(req *SystemStatsRequest, stream PingService_StreamSystemStatsServer) error {
	// Default update interval is 1 second
	updateInterval := time.Duration(req.UpdateIntervalNs)
	if updateInterval <= 0 {
		updateInterval = time.Second
	}

	// Default process limit
	processLimit := int(req.ProcessLimit)
	if processLimit <= 0 {
		processLimit = 10
	}

	s.log.Debugf("gRPC StreamSystemStats: starting with interval=%v, processes=%v, limit=%d",
		updateInterval, req.IncludeProcesses, processLimit)

	ticker := time.NewTicker(updateInterval)
	defer ticker.Stop()

	// Send initial stats immediately
	stats, err := statsCollector.Collect(stream.Context(), req.IncludeProcesses, processLimit)
	if err != nil {
		stats = &SystemStats{Error: err.Error()}
	}
	if err := stream.Send(stats); err != nil {
		return err
	}

	// Stream updates
	for {
		select {
		case <-stream.Context().Done():
			s.log.Debug("gRPC StreamSystemStats: client disconnected")
			return stream.Context().Err()
		case <-ticker.C:
			stats, err := statsCollector.Collect(stream.Context(), req.IncludeProcesses, processLimit)
			if err != nil {
				stats = &SystemStats{Error: err.Error()}
			}
			if err := stream.Send(stats); err != nil {
				return err
			}
		}
	}
}

// GetSystemStats returns a single snapshot of system stats
func (s *PingServer) GetSystemStats(ctx context.Context, req *SystemStatsRequest) (*SystemStats, error) {
	processLimit := int(req.ProcessLimit)
	if processLimit <= 0 {
		processLimit = 10
	}

	s.log.Debugf("gRPC GetSystemStats: processes=%v, limit=%d", req.IncludeProcesses, processLimit)

	stats, err := statsCollector.Collect(ctx, req.IncludeProcesses, processLimit)
	if err != nil {
		return &SystemStats{Error: err.Error()}, nil
	}

	return stats, nil
}

// StreamAppLogs subscribes to the visor's master logger and forwards
// matching entries to the gRPC client. Used by 'proxy start --verbose'
// and 'dmsg curl --verbose'.
//
// Filtering: AppName matches by _module exact equality OR app/app_name
// field. Modules matches by _module prefix. The two are OR-merged; at
// least one must be specified (AppName="*" enables wildcard mode for
// diagnostics).
//
// The visor's router/mux/setup layers attach app_name=<n> to rg-scoped
// entries (see router.Config.AppLookup), so AppName filtering scopes
// to one app's session — including layered events that happen on
// behalf of the app, not just app stdout.
//
// IncludeRouter is retained on the wire for forward compatibility but
// is currently ignored — Modules is the explicit replacement.
//
// The subscription's per-stream buffer is bounded; bursts beyond it
// are dropped and counted (cancel returns the count).
func (s *PingServer) StreamAppLogs(req *AppLogStreamRequest, stream PingService_StreamAppLogsServer) error {
	if req.AppName == "" && len(req.Modules) == 0 {
		return fmt.Errorf("app_name or modules required")
	}

	level, err := logging.LevelFromString(req.MinLevel)
	if err != nil {
		// Default to debug — verbose mode is the use case.
		level = logrus.DebugLevel
	}

	filter := logging.Filter{
		AppName:  req.AppName,
		Modules:  req.Modules,
		MinLevel: level,
	}
	// Wildcard: AppName "*" disables app-scoping for diagnostic
	// purposes — useful for verifying the stream pipe itself.
	if req.AppName == "*" {
		filter.AppName = ""
	}

	const subBuffer = 512
	ch, cancel := s.visor.SubscribeLogs(filter, subBuffer)
	if ch == nil {
		return fmt.Errorf("log broadcaster not available on this visor")
	}
	defer cancel()

	s.log.Debugf("gRPC StreamAppLogs: subscribed app=%s include_router=%v min_level=%s",
		req.AppName, req.IncludeRouter, level)

	// Sentinel: tell the client the subscription is live so it can
	// race-safely trigger downstream RPCs whose log output we want
	// to capture. Sent BEFORE entering the dispatch loop so any
	// activity from the moment the client sees this entry forward
	// is guaranteed to reach the broadcaster.
	if err := stream.Send(&AppLogEntry{
		TimestampNs: time.Now().UnixNano(),
		Subscribed:  true,
	}); err != nil {
		return err
	}

	for {
		select {
		case <-stream.Context().Done():
			return stream.Context().Err()
		case e, ok := <-ch:
			if !ok {
				return nil
			}
			if err := stream.Send(toAppLogEntry(e)); err != nil {
				return err
			}
		}
	}
}

// toAppLogEntry converts a logrus.Entry to the wire AppLogEntry. The
// _module field, when present, is hoisted into Module; remaining
// fields land in Fields as strings (best-effort fmt.Sprint).
func toAppLogEntry(e *logrus.Entry) *AppLogEntry {
	module := ""
	fields := make(map[string]string, len(e.Data))
	for k, v := range e.Data {
		if k == "_module" {
			if m, ok := v.(string); ok {
				module = m
				continue
			}
		}
		fields[k] = fmt.Sprintf("%v", v)
	}
	return &AppLogEntry{
		TimestampNs: e.Time.UnixNano(),
		Level:       e.Level.String(),
		Module:      module,
		Message:     e.Message,
		Fields:      fields,
	}
}

// StreamCalcRoutes runs a route-finder BFS server-side and streams each
// valid path back to the caller as it's discovered. The streaming wire
// shape lets callers consume routes incrementally — neither side has to
// hold every result in memory at once, which matters for unbounded
// requests on dense graphs.
//
// Source PK defaults to this visor's own. Max-hops defaults to a safe
// 5 if zero. count == 0 means "until exhausted"; the server will keep
// emitting until no more routes exist or the client disconnects.
//
// The route-finder package's StreamRoutes drives the search; the loop
// here is just transport-graph fetch + protocol marshaling.
func (s *PingServer) StreamCalcRoutes(req *CalcRoutesRequest, stream PingService_StreamCalcRoutesServer) error {
	var dstPK cipher.PubKey
	if err := dstPK.Set(req.DstPk); err != nil {
		return fmt.Errorf("invalid dst_pk: %w", err)
	}
	srcPK := s.visor.LocalPK()
	if req.SrcPk != "" {
		if err := srcPK.Set(req.SrcPk); err != nil {
			return fmt.Errorf("invalid src_pk: %w", err)
		}
	}

	maxHops := int(req.MaxHops)
	if maxHops <= 0 {
		maxHops = 5
	}
	minHops := int(req.MinHops)
	if minHops < 0 {
		minHops = 0
	}

	// Fetch the transport graph. Only TPD is wired here — DHT-source
	// support can land separately if the offline-DHT path is needed
	// from the gRPC side. The CLI's local-fallback path still has it
	// for visor-less use.
	entries, err := s.visor.FetchAllTransportEntries(stream.Context())
	if err != nil {
		return fmt.Errorf("fetch transports: %w", err)
	}
	if len(entries) == 0 {
		return fmt.Errorf("no transport entries available")
	}

	memStore := newCalcMemStore(entries)
	graph, err := routeFinder.NewGraphWithDepth(stream.Context(), memStore, srcPK, maxHops)
	if err != nil {
		return fmt.Errorf("build graph: %w", err)
	}

	queueCap := int(req.QueueCap)
	if queueCap == 0 {
		queueCap = routeFinder.DefaultMaxBFSQueue
	}
	sent := int32(0)
	streamErr := graph.StreamRoutesWithCap(stream.Context(), srcPK, dstPK, minHops, maxHops, queueCap, func(r routing.Route) bool {
		out := &CalcRoute{Hops: make([]*CalcHop, 0, len(r.Hops))}
		for _, h := range r.Hops {
			out.Hops = append(out.Hops, &CalcHop{
				TpId: h.TpID.String(),
				From: h.From.String(),
				To:   h.To.String(),
			})
		}
		if err := stream.Send(out); err != nil {
			s.log.WithError(err).Debug("StreamCalcRoutes: send failed; aborting search")
			return false
		}
		sent++
		if req.Count > 0 && sent >= req.Count {
			return false
		}
		return true
	})
	// ErrRouteNotFound is informational: the search ran clean to
	// completion without finding any matching path. Translate it to
	// nil here when at least one route was sent (hop-length retries
	// can hit ErrRouteNotFound after emitting valid paths).
	if streamErr != nil && (sent == 0 || streamErr != routeFinder.ErrRouteNotFound) {
		return streamErr
	}
	return nil
}

// calcMemStore is a tiny in-process store.Store impl for the route-finder
// package. It only needs to answer GetTransportsByEdge during graph
// construction — every other method is a stub.
type calcMemStore struct {
	byEdge map[cipher.PubKey][]*transport.Entry
}

func newCalcMemStore(entries []*transport.Entry) *calcMemStore {
	byEdge := make(map[cipher.PubKey][]*transport.Entry)
	for _, e := range entries {
		if e == nil {
			continue
		}
		byEdge[e.Edges[0]] = append(byEdge[e.Edges[0]], e)
		if e.Edges[0] != e.Edges[1] {
			byEdge[e.Edges[1]] = append(byEdge[e.Edges[1]], e)
		}
	}
	return &calcMemStore{byEdge: byEdge}
}

func (s *calcMemStore) GetTransportsByEdgeNoLatency(ctx context.Context, pk cipher.PubKey) ([]*transport.Entry, error) {
	return s.GetTransportsByEdge(ctx, pk)
}

func (s *calcMemStore) GetTransportsByEdge(_ context.Context, pk cipher.PubKey) ([]*transport.Entry, error) {
	if tps, ok := s.byEdge[pk]; ok {
		return tps, nil
	}
	return nil, tpdstore.ErrTransportNotFound
}

// Unused store.Store stubs.
func (s *calcMemStore) RegisterTransport(context.Context, *transport.SignedEntry) error {
	return nil
}
func (s *calcMemStore) RegisterTransportsBatch(context.Context, []*transport.SignedEntry) error {
	return nil
}
func (s *calcMemStore) DeregisterTransport(context.Context, uuid.UUID) error { return nil }
func (s *calcMemStore) GetTransportByID(context.Context, uuid.UUID) (*transport.Entry, error) {
	return nil, tpdstore.ErrTransportNotFound
}
func (s *calcMemStore) GetNumberOfTransports(context.Context) (map[tptypes.Type]int, error) {
	return nil, nil
}
func (s *calcMemStore) GetAllTransports(context.Context, bool) ([]*transport.Entry, error) {
	return nil, nil
}
func (s *calcMemStore) UpdateBandwidth(context.Context, string, cipher.PubKey, uint64, uint64) error {
	return nil
}
func (s *calcMemStore) UpdateLatency(context.Context, string, float64, float64, float64) error {
	return nil
}
func (s *calcMemStore) GetTransportBandwidth(context.Context, uuid.UUID, string, int) ([]tpdstore.BandwidthAggregation, error) {
	return nil, nil
}
func (s *calcMemStore) GetVisorBandwidth(context.Context, cipher.PubKey, string, int) ([]tpdstore.BandwidthAggregation, error) {
	return nil, nil
}
func (s *calcMemStore) GetAllVisorSummaries(context.Context, bool, bool) ([]tpdstore.VisorSummary, error) {
	return nil, nil
}
func (s *calcMemStore) RecordHeartbeat(context.Context, cipher.PubKey, string) error { return nil }
func (s *calcMemStore) GetDailyTimeline(context.Context, string, time.Time) map[string]string {
	return nil
}
func (s *calcMemStore) RecordTransportHeartbeat(context.Context, uuid.UUID, string, time.Time) error {
	return nil
}
func (s *calcMemStore) IngestTransportTimeline(context.Context, uuid.UUID, string, []byte) error {
	return nil
}
func (s *calcMemStore) GetTransportUptimeSummaries(context.Context, []uuid.UUID, bool, bool) ([]tpdstore.TransportUptimeSummary, error) {
	return nil, nil
}
func (s *calcMemStore) GetTransportUptimeByVisor(context.Context, cipher.PubKey, bool, bool) ([]tpdstore.TransportUptimeSummary, error) {
	return nil, nil
}
func (s *calcMemStore) GetTransportDailyTimeline(context.Context, string, time.Time) map[string]string {
	return nil
}
func (s *calcMemStore) BackupAndCleanOldBandwidth(context.Context, string) error { return nil }
func (s *calcMemStore) GetNetworkMetrics(context.Context, tpdstore.MetricsQuery) (*tpdstore.NetworkMetricResponse, error) {
	return nil, nil
}
func (s *calcMemStore) GetVisorAggregateMetrics(context.Context, []cipher.PubKey, tpdstore.MetricsQuery) (map[string]*tpdstore.VisorMetricResponse, error) {
	return nil, nil
}
func (s *calcMemStore) GetAllTransportMetrics(context.Context, tpdstore.MetricsQuery) ([]tpdstore.TransportMetric, error) {
	return nil, nil
}
func (s *calcMemStore) GetTransportMetricsByIDs(context.Context, []uuid.UUID, tpdstore.MetricsQuery) ([]tpdstore.TransportMetric, error) {
	return nil, nil
}
func (s *calcMemStore) GetTransportMetricsByVisors(context.Context, []cipher.PubKey, tpdstore.MetricsQuery) ([]tpdstore.TransportMetric, error) {
	return nil, nil
}
func (s *calcMemStore) Close() {}

// StreamRemoteSystemStats proxies system stats from a remote visor via DMSG.
// The local visor dials the remote visor over DMSG and forwards the stats stream.
func (s *PingServer) StreamRemoteSystemStats(req *RemoteSystemStatsRequest, stream PingService_StreamRemoteSystemStatsServer) error {
	var remotePK cipher.PubKey
	if err := remotePK.Set(req.RemotePk); err != nil {
		return fmt.Errorf("invalid remote public key: %w", err)
	}

	s.log.Debugf("gRPC StreamRemoteSystemStats: dialing remote visor %s over DMSG", remotePK)

	// Dial remote visor over DMSG using the local visor's DMSG client
	conn, err := s.visor.DialDmsgRPC(remotePK)
	if err != nil {
		return fmt.Errorf("failed to dial remote visor over DMSG: %w", err)
	}
	defer conn.Close() //nolint:errcheck

	// Create gRPC client over the DMSG connection
	grpcConn, err := grpc.NewClient("passthrough:///dmsg",
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(func(_ context.Context, _ string) (net.Conn, error) {
			return conn, nil
		}),
	)
	if err != nil {
		return fmt.Errorf("failed to create gRPC client: %w", err)
	}
	defer grpcConn.Close() //nolint:errcheck

	// Create the service client
	remoteClient := NewPingServiceClient(grpcConn)

	// Forward the request to the remote visor
	remoteReq := &SystemStatsRequest{
		UpdateIntervalNs: req.UpdateIntervalNs,
		IncludeProcesses: req.IncludeProcesses,
		ProcessLimit:     req.ProcessLimit,
	}

	remoteStream, err := remoteClient.StreamSystemStats(stream.Context(), remoteReq)
	if err != nil {
		return fmt.Errorf("failed to start remote stats stream: %w", err)
	}

	s.log.Debugf("gRPC StreamRemoteSystemStats: connected to remote visor %s, proxying stream", remotePK)

	// Proxy the stream
	for {
		stats, err := remoteStream.Recv()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return fmt.Errorf("remote stream error: %w", err)
		}

		if err := stream.Send(stats); err != nil {
			return err
		}
	}
}

// StreamGroupMessages opens a long-lived subscription to the visor's
// group inbox. The server registers with the inbox via the VisorAPI
// adapter, then forwards every delivered message to the client over
// the gRPC stream. Stream lifetime is the subscription lifetime — no
// per-conn deadline applies (the deadline-driven 5-minute reconnect
// pattern is exactly what this RPC was added to escape).
//
// Sequence:
//  1. Register the subscription on the inbox (bounded buffer). This
//     happens BEFORE the backlog snapshot — any message arriving
//     during the snapshot read is captured by the live channel and
//     deduped against the backlog via lastSentNs.
//  2. Send a Subscribed sentinel so the client can confirm the stream
//     is live before downstream actions (mirrors the AppLogStream
//     pattern; lets the CLI gate Ctrl+C handler installation, etc.).
//  3. If req.SinceTimestampNs > 0, drain inbox-buffered messages with
//     TS > since via SnapshotGroupMessagesAfterNs. Each replayed
//     message bumps lastSentNs so the same message arriving on the
//     live channel (the subscribe/snapshot race window) is suppressed.
//     Bounded by the inbox ring size (groupInboxCap) — a client
//     disconnected longer than the ring turnover gets only the tail.
//  4. Loop: select on stream.Context().Done() or the subscription
//     channel; forward each message to the wire when m.TimestampNs >
//     lastSentNs.
//
// Filter: req.GroupId, when set, scopes the stream to one group. Empty
// matches every group the visor is in.
// mergeGroupBacklog interleaves the durable history snapshot with the
// in-memory inbox snapshot in TimestampNs order, dropping the
// duplicates that result from the inbox-and-history both observing
// the same delivery. Output is chronological (oldest first) so the
// streaming handler's lastSentNs gate emits each message exactly
// once.
//
// Dedup key: (GroupID, TimestampNs, SenderPK). The deliver path
// writes one history record + one inbox entry per Message, both
// carrying the same triple — exact-match dedup is sufficient. Body
// is not part of the key because (a) it's expensive to compare on a
// hot path and (b) two messages with the same triple but different
// bodies would already be a corruption signal worth surfacing
// upstream rather than silently merging.
//
// Pre-condition: each input is itself in TimestampNs ascending order.
// The history walker (BoltStore.ListGroupSince) walks the bbolt
// cursor forward from a tsKey-based Seek; the inbox snapshot iterates
// the ring forward. Both satisfy the pre-condition by construction.
func mergeGroupBacklog(history, inbox []GroupMessageData) []GroupMessageData {
	if len(history) == 0 {
		return inbox
	}
	if len(inbox) == 0 {
		return history
	}
	out := make([]GroupMessageData, 0, len(history)+len(inbox))
	seen := make(map[backlogKey]struct{}, len(history)+len(inbox))
	emit := func(m GroupMessageData) {
		k := backlogKey{group: m.GroupID, ts: m.TimestampNs, sender: m.SenderPK}
		if _, dup := seen[k]; dup {
			return
		}
		seen[k] = struct{}{}
		out = append(out, m)
	}
	i, j := 0, 0
	for i < len(history) && j < len(inbox) {
		if history[i].TimestampNs <= inbox[j].TimestampNs {
			emit(history[i])
			i++
		} else {
			emit(inbox[j])
			j++
		}
	}
	for ; i < len(history); i++ {
		emit(history[i])
	}
	for ; j < len(inbox); j++ {
		emit(inbox[j])
	}
	return out
}

type backlogKey struct {
	group  string
	ts     int64
	sender string
}

func (s *PingServer) StreamGroupMessages(req *GroupMessagesRequest, stream PingService_StreamGroupMessagesServer) error {
	if req == nil {
		return fmt.Errorf("nil request")
	}

	const subBuffer = 256
	ch, cancel := s.visor.SubscribeGroupMessages(subBuffer)
	if ch == nil {
		return fmt.Errorf("group inbox not available on this visor")
	}
	// On tear-down, log the subscriber's final dropCount at WARN if
	// non-zero. Pre-fix this number was silently discarded, leaving a
	// gap where an operator watching subscriber_alive=true could
	// reasonably believe the receive path was healthy while the CLI
	// listener was actually missing messages dropped between
	// inbox.deliver and the gRPC stream's bounded channel. Inbox-
	// wide rolling total is also surfaced live via GroupInfo
	// .SubDropCount (#groupInbox.SubDropCount); the per-subscriber
	// number from cancel() is the just-departing stream's
	// contribution, useful for correlating a specific reconnect's
	// shape with the rolling visor-wide total.
	defer func() {
		dropped := cancel()
		if dropped > 0 {
			s.log.Warnf("gRPC StreamGroupMessages: subscriber teardown drop_count=%d group_id=%q (inbox→stream channel was full during fan-out; messages survive in the inbox ring + history sink, reconnect with SinceTimestampNs replays them)",
				dropped, req.GroupId)
		}
	}()

	s.log.Debugf("gRPC StreamGroupMessages: subscribed group_id=%q since_ns=%d", req.GroupId, req.SinceTimestampNs)

	// Sentinel: tell the client the subscription is live so it can
	// safely drop fallback paths / install signal handlers / etc.
	// before the first real message arrives. Sent BEFORE entering
	// the dispatch loop.
	if err := stream.Send(&GroupMessageEvent{
		TimestampNs: time.Now().UnixNano(),
		Subscribed:  true,
	}); err != nil {
		return err
	}
	s.visor.RecordGroupStreamSend()

	// lastSentNs deduplicates between the backlog snapshot and the
	// live channel for messages that landed in the
	// subscribe-but-not-yet-read window. Initialized to the client's
	// since cursor so old messages already seen on a prior stream
	// are never re-emitted.
	lastSentNs := req.SinceTimestampNs

	// Backlog replay. Skips when client didn't pass a since (fresh
	// listen — current behavior, only live messages).
	if req.SinceTimestampNs > 0 {
		// Two-source backlog:
		//   inbox    — in-memory ring (~256 messages, every group),
		//              bounded by groupInboxCap; long disconnects roll past.
		//   history  — durable bbolt sink (per-group buckets), opt-in via
		//              the operator's GroupHistoryDB config. nil/empty
		//              when persistence is off.
		//
		// Strategy: query both, merge by TimestampNs, dedup against
		// lastSentNs. The inbox is a strict superset of "messages
		// landed since this visor's startup"; history is a strict
		// superset of "messages landed since persistence began".
		// In steady state every inbox entry is also in history (same
		// deliver path writes both), so the merge mostly de-dupes
		// at the lastSentNs gate. The gain is correctness on
		// disconnect gaps longer than the inbox ring — without
		// history, those messages were silently lost from the
		// replay even though they survived on disk.
		//
		// History is queried only when the request scopes to a single
		// group (req.GroupId set). The history store is per-group; a
		// "stream every group" request fans across an unbounded set,
		// which would defeat the bounded-cost intent of the inbox
		// fallback. Streaming-every-group is the same case
		// pre-this-PR — inbox only — and that's the right default
		// without operator opt-in to broader replay.
		var history []GroupMessageData
		if req.GroupId != "" {
			history = s.visor.SnapshotGroupHistoryAfterNs(req.GroupId, req.SinceTimestampNs)
		}
		backlog := s.visor.SnapshotGroupMessagesAfterNs(req.SinceTimestampNs)
		merged := mergeGroupBacklog(history, backlog)
		// Diagnostic log: tell history-hit, inbox-hit, and merged
		// counts apart so operators can confirm the history fallback
		// actually fired when the gap was long.
		s.log.Debugf("gRPC StreamGroupMessages: replay since_ns=%d group=%q inbox=%d history=%d merged=%d",
			req.SinceTimestampNs, req.GroupId, len(backlog), len(history), len(merged))
		for _, m := range merged {
			if req.GroupId != "" && m.GroupID != req.GroupId {
				continue
			}
			if m.TimestampNs <= lastSentNs {
				continue
			}
			if err := stream.Send(&GroupMessageEvent{
				TimestampNs: m.TimestampNs,
				GroupId:     m.GroupID,
				SenderPk:    m.SenderPK,
				Body:        m.Body,
			}); err != nil {
				return err
			}
			s.visor.RecordGroupStreamSend()
			lastSentNs = m.TimestampNs
		}
	}

	for {
		select {
		case <-stream.Context().Done():
			return stream.Context().Err()
		case m, ok := <-ch:
			if !ok {
				return nil
			}
			// Server-side filter by group_id. The inbox fans every
			// message to every subscriber; filtering here avoids
			// per-stream filtering layers in the inbox itself.
			if req.GroupId != "" && m.GroupID != req.GroupId {
				continue
			}
			// Dedup against the backlog replay above. A message
			// landing in the inbox during the subscribe-but-not-yet-
			// snapshot window is delivered on the live channel AND
			// captured in the snapshot; lastSentNs ensures it goes
			// out exactly once.
			if m.TimestampNs <= lastSentNs {
				continue
			}
			if err := stream.Send(&GroupMessageEvent{
				TimestampNs: m.TimestampNs,
				GroupId:     m.GroupID,
				SenderPk:    m.SenderPK,
				Body:        m.Body,
			}); err != nil {
				return err
			}
			s.visor.RecordGroupStreamSend()
			lastSentNs = m.TimestampNs
		}
	}
}
