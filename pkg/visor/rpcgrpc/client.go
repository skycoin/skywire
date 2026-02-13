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
type PingResultCallback func(seq int32, latency time.Duration, isSetup bool, routeHopDetails []RouteHopDetail, err error)

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
	stream, err := c.client.StreamPing(ctx, &PingRequest{
		PublicKey:      pk,
		Tries:          tries,
		PacketSizeKb:   pcktSize,
		LocalRoute:     localRoute,
		PingTimeoutNs:  timeout.Nanoseconds(),
		SetupTimeoutNs: setupTimeout.Nanoseconds(),
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
		cb(result.Sequence, time.Duration(result.LatencyNs), result.IsSetup, hopDetails, pingErr)
	}
}

// StreamDmsgPing performs dmsg pings and calls the callback for each result
// timeout applies only to the ping phase (after dial), 0 means no timeout
func (c *PingClient) StreamDmsgPing(ctx context.Context, pk string, tries int32, pcktSize int32, timeout time.Duration, cb PingResultCallback) error {
	stream, err := c.client.StreamDmsgPing(ctx, &PingRequest{
		PublicKey:     pk,
		Tries:         tries,
		PacketSizeKb:  pcktSize,
		PingTimeoutNs: timeout.Nanoseconds(),
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
		// DMSG pings don't have route hops
		cb(result.Sequence, time.Duration(result.LatencyNs), result.IsSetup, nil, pingErr)
	}
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
