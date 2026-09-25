// Package router pkg/router/route_group_ping.go c2-net-routing
package router

import (
	"context"
	"errors"
	"time"

	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/transport"
)

func (rg *RouteGroup) sendPing() error {
	rg.mu.Lock()

	if len(rg.tps) == 0 || len(rg.fwd) == 0 {
		rg.mu.Unlock()
		// if no transports, no rules, then no latency probe
		return nil
	}

	tp := rg.tps[0]
	rule := rg.fwd[0]
	rg.mu.Unlock()

	if tp == nil {
		return nil
	}

	throughput := rg.networkStats.RemoteThroughput()
	timestamp := time.Now().UTC().UnixNano() / int64(time.Millisecond)
	rg.networkStats.SetDownloadSpeed(uint32(throughput)) //nolint: gosec

	packet := routing.MakePingPacket(rule.NextRouteID(), timestamp, throughput)

	return rg.writePacket(context.Background(), tp, packet, rule.KeyRouteID())
}

func (rg *RouteGroup) sendPong(timestamp int64) error {
	rg.mu.Lock()

	if len(rg.tps) == 0 || len(rg.fwd) == 0 {
		rg.mu.Unlock()
		// if no transports, no rules, then no latency probe
		return nil
	}

	tp := rg.tps[0]
	rule := rg.fwd[0]
	rg.mu.Unlock()

	if tp == nil {
		return nil
	}

	packet := routing.MakePongPacket(rule.NextRouteID(), timestamp)

	return rg.writePacket(context.Background(), tp, packet, rule.KeyRouteID())
}

func (rg *RouteGroup) sendKeepAlive() error {
	rg.mu.Lock()
	defer rg.mu.Unlock()

	if len(rg.tps) == 0 || len(rg.fwd) == 0 {
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), closeRoutineTimeout)
	defer cancel()

	// Track successes — in mux mode, only fail if ALL transports are dead.
	// A single dead transport in a mux group should not kill the entire connection.
	anySuccess := false
	var lastErr error

	for i := 0; i < len(rg.tps); i++ {
		tp := rg.tps[i]
		rule := rg.fwd[i]

		if tp == nil || tp.IsClosed() {
			continue
		}

		packet := routing.MakeKeepAlivePacket(rule.NextRouteID())

		if err := rg.writePacket(ctx, tp, packet, rule.KeyRouteID()); err != nil {
			lastErr = err
			rg.logger.Debugf("Keepalive failed on transport %v: %v", tp.Entry.ID, err)
			continue
		}
		anySuccess = true
	}

	if !anySuccess {
		if lastErr != nil {
			return lastErr
		}
		return ErrNoSuitableTransport
	}

	return nil
}

// MeasureLatency performs multiple ping/pong measurements and returns statistics.
// It sends 'count' pings, waits for pongs, and calculates min/max/avg latency.
// Returns the stats and any error. Partial results are returned if some pings fail.
func (rg *RouteGroup) MeasureLatency(ctx context.Context, count int) (min, max, avg float64, err error) {
	if count <= 0 {
		count = 5 // Default to 5 measurements
	}

	// Set up channel for receiving pong responses
	pongCh := make(chan float64, count)
	rg.pendingPongMu.Lock()
	rg.pendingPongCh = pongCh
	rg.pendingPongMu.Unlock()

	defer func() {
		rg.pendingPongMu.Lock()
		rg.pendingPongCh = nil
		rg.pendingPongMu.Unlock()
		close(pongCh)
	}()

	var measurements []float64
	timeout := 5 * time.Second // Timeout per ping

	for i := 0; i < count; i++ {
		// Send ping
		if err := rg.sendPing(); err != nil {
			rg.logger.WithError(err).Debugf("Failed to send ping %d/%d", i+1, count)
			continue
		}

		// Wait for pong with timeout
		select {
		case latencyMs := <-pongCh:
			// Drop samples outside (0, transport.MaxReasonableRTTMs]:
			//   - 0 seeds min/max at 0, producing {min:0, max:X, avg:Y}
			//     that downstream consumers reject or display oddly.
			//   - Stale pongs from earlier rounds (or from the periodic
			//     pingLoop) can land in this buffered channel after a
			//     long delay and produce 30+ second readings that
			//     would pin Max indefinitely on the underlying tp.
			if latencyMs <= 0 {
				rg.logger.Debugf("Ping %d/%d: dropped non-positive sample (%.6f ms)", i+1, count, latencyMs)
				continue
			}
			if latencyMs > transport.MaxReasonableRTTMs {
				rg.logger.Debugf("Ping %d/%d: dropped outlier sample (%.0f ms) — likely stale pong", i+1, count, latencyMs)
				continue
			}
			measurements = append(measurements, latencyMs)
			rg.logger.Debugf("Ping %d/%d: %.2f ms", i+1, count, latencyMs)
		case <-time.After(timeout):
			rg.logger.Debugf("Ping %d/%d timed out", i+1, count)
		case <-ctx.Done():
			return 0, 0, 0, ctx.Err()
		case <-rg.closed:
			return 0, 0, 0, errors.New("route group closed")
		}

		// Small delay between pings to avoid overwhelming the connection
		if i < count-1 {
			time.Sleep(100 * time.Millisecond)
		}
	}

	if len(measurements) == 0 {
		return 0, 0, 0, errors.New("no successful ping measurements")
	}

	// Calculate statistics
	min = measurements[0]
	max = measurements[0]
	var sum float64
	for _, m := range measurements {
		if m < min {
			min = m
		}
		if m > max {
			max = m
		}
		sum += m
	}
	avg = sum / float64(len(measurements))

	return min, max, avg, nil
}
