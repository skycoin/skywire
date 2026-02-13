// Package rpcgrpc provides gRPC streaming services for the visor
package rpcgrpc

import (
	"fmt"
	"time"

	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/logging"
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
	DialDmsgPing(pk cipher.PubKey) error
	DmsgPingOnce(conf PingConf) (time.Duration, error)
	DmsgPingOnceWithEcho(conf PingConf, echoFull bool) (bytesSent, bytesReceived uint64, latency time.Duration, err error)
	StopDmsgPing(pk cipher.PubKey) error
}

// PingConf mirrors visor.PingConfig to avoid import cycles
type PingConf struct {
	PK         cipher.PubKey
	Tries      int
	PcktSize   int
	LocalRoute bool
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

	conf := PingConf{
		PK:         pk,
		Tries:      int(req.Tries),
		PcktSize:   int(req.PacketSizeKb),
		LocalRoute: req.LocalRoute,
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

	// Send setup time as first result
	if err := stream.Send(&PingResult{
		Sequence:        0,
		LatencyNs:       setupTime.Nanoseconds(),
		IsSetup:         true,
		RouteHops:       routeHops,
		RouteHopDetails: routeHopDetails,
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

	// Dial first
	s.log.Debugf("gRPC StreamDmsgPing: dialing %s", pk)
	setupStart := time.Now()
	if err := s.visor.DialDmsgPing(pk); err != nil {
		return fmt.Errorf("dial dmsg ping failed: %w", err)
	}
	setupTime := time.Since(setupStart)

	// Send setup time as first result
	if err := stream.Send(&PingResult{
		Sequence:  0,
		LatencyNs: setupTime.Nanoseconds(),
		IsSetup:   true,
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
