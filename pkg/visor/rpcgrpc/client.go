package rpcgrpc

import (
	"context"
	"fmt"
	"io"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// RouteHopDetail contains detailed information about a route hop for display.
type RouteHopDetail struct {
	TpID   string // Transport ID
	From   string // Source public key
	To     string // Destination public key
	TpType string // Transport type (stcpr, sudph, dmsg)
}

// PingResultCallback is called for each ping result received from the stream.
// routeHopDetails is only set for the setup message (isSetup=true) for skywire route pings.
// dmsgServerPK is only set for DMSG pings and contains the server used for the connection.
// routeCalcTime is the time spent calculating the route (local route mode only, isSetup=true).
type PingResultCallback func(seq int32, latency time.Duration, isSetup bool, routeHopDetails []RouteHopDetail, dmsgServerPK string, routeCalcTime time.Duration, err error)

// PingClient provides streaming ping operations via gRPC
type PingClient struct {
	conn   *grpc.ClientConn
	client PingServiceClient
}

// NewPingClient creates a new gRPC ping client
func NewPingClient(addr string) (*PingClient, error) {
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, fmt.Errorf("failed to connect to gRPC server: %w", err)
	}
	return &PingClient{
		conn:   conn,
		client: NewPingServiceClient(conn),
	}, nil
}

// Close closes the gRPC connection
func (c *PingClient) Close() error {
	return c.conn.Close()
}

// StreamPing performs pings and calls the callback for each result
// timeout applies only to the ping phase (after route setup), 0 means no timeout
// setupTimeout applies to route setup phase, 0 means no timeout
func (c *PingClient) StreamPing(ctx context.Context, pk string, tries int32, pcktSize int32, localRoute bool, timeout time.Duration, setupTimeout time.Duration, cb PingResultCallback) error {
	return c.StreamPingWithTransport(ctx, pk, tries, pcktSize, localRoute, timeout, setupTimeout, "", cb)
}

// StreamPingWithTransport performs pings using a specific transport (skips route calculation)
// If transportID is empty, normal route calculation is used
func (c *PingClient) StreamPingWithTransport(ctx context.Context, pk string, tries int32, pcktSize int32, localRoute bool, timeout time.Duration, setupTimeout time.Duration, transportID string, cb PingResultCallback) error {
	stream, err := c.client.StreamPing(ctx, &PingRequest{
		PublicKey:      pk,
		Tries:          tries,
		PacketSizeKb:   pcktSize,
		LocalRoute:     localRoute,
		PingTimeoutNs:  timeout.Nanoseconds(),
		SetupTimeoutNs: setupTimeout.Nanoseconds(),
		TransportId:    transportID,
	})
	if err != nil {
		return fmt.Errorf("failed to start ping stream: %w", err)
	}

	for {
		result, err := stream.Recv()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return fmt.Errorf("stream error: %w", err)
		}

		var pingErr error
		if result.Error != "" {
			pingErr = fmt.Errorf("%s", result.Error)
		}

		// Convert proto RouteHop to RouteHopDetail
		var hopDetails []RouteHopDetail
		if result.RouteHopDetails != nil {
			hopDetails = make([]RouteHopDetail, len(result.RouteHopDetails))
			for i, h := range result.RouteHopDetails {
				hopDetails[i] = RouteHopDetail{
					TpID:   h.TpId,
					From:   h.From,
					To:     h.To,
					TpType: h.TpType,
				}
			}
		}
		routeCalcTime := time.Duration(result.RouteCalcTimeNs)
		cb(result.Sequence, time.Duration(result.LatencyNs), result.IsSetup, hopDetails, "", routeCalcTime, pingErr)
	}
}

// StreamPingWithRoute performs pings using a specific route (skips route calculation)
// forwardHops and reverseHops define the exact path to use
func (c *PingClient) StreamPingWithRoute(ctx context.Context, pk string, tries int32, pcktSize int32, localRoute bool, timeout time.Duration, setupTimeout time.Duration, forwardHops []RouteHopDetail, reverseHops []RouteHopDetail, cb PingResultCallback) error {
	// Convert RouteHopDetail to proto RouteHop
	var protoForward, protoReverse []*RouteHop
	for _, h := range forwardHops {
		protoForward = append(protoForward, &RouteHop{
			TpId:   h.TpID,
			From:   h.From,
			To:     h.To,
			TpType: h.TpType,
		})
	}
	for _, h := range reverseHops {
		protoReverse = append(protoReverse, &RouteHop{
			TpId:   h.TpID,
			From:   h.From,
			To:     h.To,
			TpType: h.TpType,
		})
	}

	stream, err := c.client.StreamPing(ctx, &PingRequest{
		PublicKey:      pk,
		Tries:          tries,
		PacketSizeKb:   pcktSize,
		LocalRoute:     localRoute,
		PingTimeoutNs:  timeout.Nanoseconds(),
		SetupTimeoutNs: setupTimeout.Nanoseconds(),
		ForwardHops:    protoForward,
		ReverseHops:    protoReverse,
	})
	if err != nil {
		return fmt.Errorf("failed to start ping stream: %w", err)
	}

	for {
		result, err := stream.Recv()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return fmt.Errorf("stream error: %w", err)
		}

		var pingErr error
		if result.Error != "" {
			pingErr = fmt.Errorf("%s", result.Error)
		}

		// Convert proto RouteHop to RouteHopDetail
		var hopDetails []RouteHopDetail
		if result.RouteHopDetails != nil {
			hopDetails = make([]RouteHopDetail, len(result.RouteHopDetails))
			for i, h := range result.RouteHopDetails {
				hopDetails[i] = RouteHopDetail{
					TpID:   h.TpId,
					From:   h.From,
					To:     h.To,
					TpType: h.TpType,
				}
			}
		}
		routeCalcTime := time.Duration(result.RouteCalcTimeNs)
		cb(result.Sequence, time.Duration(result.LatencyNs), result.IsSetup, hopDetails, "", routeCalcTime, pingErr)
	}
}

// StreamDmsgPing performs dmsg pings and calls the callback for each result
// timeout applies only to the ping phase (after dial), 0 means no timeout
// dmsgServerPK optionally specifies which DMSG server to dial through (empty string for auto-select)
func (c *PingClient) StreamDmsgPing(ctx context.Context, pk string, tries int32, pcktSize int32, timeout time.Duration, dmsgServerPK string, cb PingResultCallback) error {
	stream, err := c.client.StreamDmsgPing(ctx, &PingRequest{
		PublicKey:     pk,
		Tries:         tries,
		PacketSizeKb:  pcktSize,
		PingTimeoutNs: timeout.Nanoseconds(),
		DmsgServerPk:  dmsgServerPK,
	})
	if err != nil {
		return fmt.Errorf("failed to start dmsg ping stream: %w", err)
	}

	for {
		result, err := stream.Recv()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return fmt.Errorf("stream error: %w", err)
		}

		var pingErr error
		if result.Error != "" {
			pingErr = fmt.Errorf("%s", result.Error)
		}
		// DMSG pings don't have route hops or route calc time, but include server PK
		cb(result.Sequence, time.Duration(result.LatencyNs), result.IsSetup, nil, result.DmsgServerPk, 0, pingErr)
	}
}

// GetRemoteDmsgServers returns the DMSG servers a remote visor is connected to
func (c *PingClient) GetRemoteDmsgServers(ctx context.Context, pk string) ([]string, error) {
	resp, err := c.client.GetRemoteDmsgServers(ctx, &DmsgServersRequest{
		PublicKey: pk,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get remote dmsg servers: %w", err)
	}
	return resp.ServerPks, nil
}

// BandwidthProgressCallback is called for each bandwidth progress update.
type BandwidthProgressCallback func(bytesSent, bytesReceived uint64, elapsed time.Duration, uploadSpeed, downloadSpeed float64, isFinal bool, err error)

// StreamBandwidthTest performs a bandwidth test over skywire route and calls callback for each progress update
func (c *PingClient) StreamBandwidthTest(ctx context.Context, pk string, duration time.Duration, pcktSize int32, localRoute bool, cb BandwidthProgressCallback) error {
	stream, err := c.client.StreamBandwidthTest(ctx, &BandwidthRequest{
		PublicKey:    pk,
		DurationNs:   duration.Nanoseconds(),
		PacketSizeKb: pcktSize,
		LocalRoute:   localRoute,
	})
	if err != nil {
		return fmt.Errorf("failed to start bandwidth test stream: %w", err)
	}

	for {
		result, err := stream.Recv()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return fmt.Errorf("stream error: %w", err)
		}

		var testErr error
		if result.Error != "" {
			testErr = fmt.Errorf("%s", result.Error)
		}
		cb(result.BytesSent, result.BytesReceived, time.Duration(result.ElapsedNs),
			result.UploadSpeed, result.DownloadSpeed, result.IsFinal, testErr)
	}
}

// StreamDmsgBandwidthTest performs a bandwidth test over dmsg and calls callback for each progress update
func (c *PingClient) StreamDmsgBandwidthTest(ctx context.Context, pk string, duration time.Duration, pcktSize int32, cb BandwidthProgressCallback) error {
	stream, err := c.client.StreamDmsgBandwidthTest(ctx, &BandwidthRequest{
		PublicKey:    pk,
		DurationNs:   duration.Nanoseconds(),
		PacketSizeKb: pcktSize,
	})
	if err != nil {
		return fmt.Errorf("failed to start dmsg bandwidth test stream: %w", err)
	}

	for {
		result, err := stream.Recv()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return fmt.Errorf("stream error: %w", err)
		}

		var testErr error
		if result.Error != "" {
			testErr = fmt.Errorf("%s", result.Error)
		}
		cb(result.BytesSent, result.BytesReceived, time.Duration(result.ElapsedNs),
			result.UploadSpeed, result.DownloadSpeed, result.IsFinal, testErr)
	}
}

// SystemStatsCallback is called for each system stats update received from the stream.
type SystemStatsCallback func(stats *SystemStats)

// StreamSystemStats streams system stats and calls the callback for each update.
// updateInterval specifies how often to receive updates (default 1s).
// includeProcesses controls whether to include top processes (more expensive).
// processLimit limits the number of processes to include (default 10).
func (c *PingClient) StreamSystemStats(ctx context.Context, updateInterval time.Duration, includeProcesses bool, processLimit int32, cb SystemStatsCallback) error {
	stream, err := c.client.StreamSystemStats(ctx, &SystemStatsRequest{
		UpdateIntervalNs: updateInterval.Nanoseconds(),
		IncludeProcesses: includeProcesses,
		ProcessLimit:     processLimit,
	})
	if err != nil {
		return fmt.Errorf("failed to start system stats stream: %w", err)
	}

	for {
		stats, err := stream.Recv()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return fmt.Errorf("stream error: %w", err)
		}
		cb(stats)
	}
}

// GetSystemStats returns a single snapshot of system stats.
func (c *PingClient) GetSystemStats(ctx context.Context, includeProcesses bool, processLimit int32) (*SystemStats, error) {
	stats, err := c.client.GetSystemStats(ctx, &SystemStatsRequest{
		IncludeProcesses: includeProcesses,
		ProcessLimit:     processLimit,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get system stats: %w", err)
	}
	return stats, nil
}

// StreamRemoteSystemStats streams system stats from a remote visor via DMSG.
// The local visor proxies the connection to the remote visor using its DMSG client.
func (c *PingClient) StreamRemoteSystemStats(ctx context.Context, remotePK string, updateInterval time.Duration, includeProcesses bool, processLimit int32, cb SystemStatsCallback) error {
	stream, err := c.client.StreamRemoteSystemStats(ctx, &RemoteSystemStatsRequest{
		RemotePk:         remotePK,
		UpdateIntervalNs: updateInterval.Nanoseconds(),
		IncludeProcesses: includeProcesses,
		ProcessLimit:     processLimit,
	})
	if err != nil {
		return fmt.Errorf("failed to start remote system stats stream: %w", err)
	}

	for {
		stats, err := stream.Recv()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return fmt.Errorf("stream error: %w", err)
		}
		cb(stats)
	}
}
