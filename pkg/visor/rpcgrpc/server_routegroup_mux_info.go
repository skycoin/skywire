// Package rpcgrpc pkg/visor/rpcgrpc/server_routegroup_mux_info.go c3-vis-core
// server-side implementation of the StreamRouteGroupMuxInfo gRPC RPC.
//
// This is the server-streaming counterpart of the unary RouteGroupMuxInfo
// net/rpc call. 'cli proxy mux plot' (default, watch-a-running-app mode)
// used to poll that unary RPC once per --interval — a gob round-trip per
// tick that read choppy next to the --pk StreamMuxBandwidth path. This RPC
// pushes the same per-leg breakdown on a server-side ticker so the plot
// updates smoothly/continuously without per-tick connection overhead.
package rpcgrpc

import (
	"time"
)

const (
	// defaultRouteGroupMuxSampleInterval is the emit cadence when the
	// request leaves sample_interval_ns at 0.
	defaultRouteGroupMuxSampleInterval = 500 * time.Millisecond
	// minRouteGroupMuxSampleInterval floors the cadence so a client can't
	// spin the visor with a sub-millisecond interval.
	minRouteGroupMuxSampleInterval = 100 * time.Millisecond
)

// StreamRouteGroupMuxInfo streams a running app's per-leg mux telemetry
// on a fixed cadence. See ping.proto for the wire contract.
//
// The first sample fires immediately (so the client renders without
// waiting a full interval), then every sample_interval_ns until the
// client disconnects / cancels. A transient absent route group is NOT an
// error — the handler emits an empty sample and keeps sampling, so the
// plot shows an empty frame until the app dials.
func (s *PingServer) StreamRouteGroupMuxInfo(req *StreamRouteGroupMuxInfoRequest, stream PingService_StreamRouteGroupMuxInfoServer) error {
	ctx := stream.Context()

	interval := time.Duration(req.SampleIntervalNs)
	if interval <= 0 {
		interval = defaultRouteGroupMuxSampleInterval
	}
	if interval < minRouteGroupMuxSampleInterval {
		interval = minRouteGroupMuxSampleInterval
	}

	send := func() error {
		infos, err := s.visor.RouteGroupMuxInfo(req.AppName)
		if err != nil {
			// Never tear the stream down on a transient "no route group
			// yet" — send an empty sample and keep going so the plot
			// renders an empty frame instead of dying. Real errors are
			// rare here (the visor returns an empty slice when the app
			// has no rg); log for diagnostics either way.
			s.log.Debugf("StreamRouteGroupMuxInfo(app=%q): %v", req.AppName, err)
			infos = nil
		}
		return stream.Send(&RouteGroupMuxInfoSample{
			TimestampNs: time.Now().UnixNano(),
			RouteGroups: convertRouteGroupMuxInfo(infos),
		})
	}

	// Immediate first sample, then tick.
	if err := send(); err != nil {
		return err
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if err := send(); err != nil {
				return err
			}
		}
	}
}

// convertRouteGroupMuxInfo projects the rpcgrpc-local mirror slice the
// VisorAPI returns into the proto wire shape.
func convertRouteGroupMuxInfo(infos []RouteGroupMuxInfoData) []*RouteGroupMuxInfo {
	if len(infos) == 0 {
		return nil
	}
	out := make([]*RouteGroupMuxInfo, 0, len(infos))
	for _, info := range infos {
		g := &RouteGroupMuxInfo{
			MuxEnabled:  info.MuxEnabled,
			SackEnabled: info.SACKEnabled,
			Legs:        make([]*RouteGroupMuxLeg, 0, len(info.Legs)),
		}
		for _, leg := range info.Legs {
			g.Legs = append(g.Legs, &RouteGroupMuxLeg{
				RouteIndex:    int32(leg.Index), //nolint:gosec // leg index fits int32
				TransportId:   leg.TransportID,
				TransportKind: leg.TpType,
				RemotePk:      leg.RemotePK,
				LatencyMs:     leg.LatencyMS,
				SentBytes:     leg.SentBytes,
				SentPackets:   leg.SentPackets,
				RecvBytes:     leg.RecvBytes,
				RecvPackets:   leg.RecvPackets,
				Retransmits:   leg.Retransmits,
				Alive:         leg.Alive,
				Standby:       leg.Standby,
			})
		}
		out = append(out, g)
	}
	return out
}
