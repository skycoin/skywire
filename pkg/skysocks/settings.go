// Package skysocks pkg/skysocks/settings.go
//
// The client's side of the live settings surface. The registry itself lives in
// pkg/skysocks/skysettings (import-free, so the CLI can read the catalog); this
// file is the seam between it and the client:
//
//   - the accessors the use sites read, each defaulting to the constant the
//     package compiled with, so an unset client is byte-for-byte today's;
//   - pullSettings, hung on the keepalive loop's existing RTT tick — the app is
//     the RPC client, so the running app PULLS what the visor holds for it.
//
// The two range-split knobs are OVERRIDES rather than plain values: chunk size
// and concurrency already have boot flags (--range-chunk-kib,
// --range-concurrency), so the knob wins only once it has been explicitly set.
package skysocks

import (
	"time"

	"github.com/skycoin/skywire/pkg/skysocks/skysettings"
)

func setPoolFillInterval() time.Duration {
	return skysettings.Dur(skysettings.PoolFillInterval)
}
func setPoolRetryBackoffBase() time.Duration {
	return skysettings.Dur(skysettings.PoolRetryBackoffMin)
}
func setPoolRetryBackoffMax() time.Duration {
	return skysettings.Dur(skysettings.PoolRetryBackoffMax)
}
func setPoolRetryRounds() int { return skysettings.Count(skysettings.PoolRetryRounds) }
func setStandbyRTTStale() time.Duration {
	return skysettings.Dur(skysettings.PoolStandbyRTTStale)
}

func setTunnelRTTProbeInterval() time.Duration {
	return skysettings.Dur(skysettings.TunnelProbeInterval)
}
func setLivenessProbeInterval() time.Duration {
	return skysettings.Dur(skysettings.TunnelLivenessInterval)
}
func setTunnelRTTAlpha() float64 { return skysettings.Ratio(skysettings.TunnelRTTAlpha) }
func setTunnelPromoteInterval() time.Duration {
	return skysettings.Dur(skysettings.TunnelPromoteInterval)
}
func setTunnelPromoteMargin() float64 { return skysettings.Ratio(skysettings.TunnelPromoteMargin) }
func setTunnelPromoteHold() time.Duration {
	return skysettings.Dur(skysettings.TunnelPromoteHold)
}
func setTunnelParkMinHold() time.Duration {
	return skysettings.Dur(skysettings.TunnelParkMinHold)
}
func setTunnelAuditionWindow() time.Duration {
	return skysettings.Dur(skysettings.TunnelAuditionWindow)
}
func setTunnelAuditionEvery() time.Duration {
	return skysettings.Dur(skysettings.TunnelAuditionEvery)
}
func setExitOpenPenalty() time.Duration {
	return skysettings.Dur(skysettings.TunnelExitOpenPenalty)
}
func setMeterSampleMin() time.Duration { return skysettings.Dur(skysettings.TunnelMeterSampleMin) }
func setMeterCapDecay() float64        { return skysettings.Ratio(skysettings.TunnelMeterCapDecay) }
func setMeterFresh() time.Duration     { return skysettings.Dur(skysettings.TunnelMeterFresh) }

func setChunkRetryBudget() time.Duration { return skysettings.Dur(skysettings.ChunkRetryBudget) }
func setChunkIdleTimeout() time.Duration { return skysettings.Dur(skysettings.ChunkIdleTimeout) }
func setChunkFreeRetries() int           { return skysettings.Count(skysettings.ChunkFreeRetries) }
func setChunkOutstandingFactor() int {
	return skysettings.Count(skysettings.ChunkOutstandingFactor)
}

// rsChunkSize is the range-split chunk ceiling in force: the knob when it has
// been set, otherwise whatever --range-chunk-kib configured this client with.
func (c *Client) rsChunkSize() int64 {
	if skysettings.IsSet(skysettings.ChunkMaxBytes) {
		return skysettings.Bytes(skysettings.ChunkMaxBytes)
	}
	return c.rs.chunkSize
}

// rsConcurrency is the range-split fetch concurrency in force; see rsChunkSize.
func (c *Client) rsConcurrency() int {
	if skysettings.IsSet(skysettings.ChunkConcurrency) {
		return skysettings.Count(skysettings.ChunkConcurrency)
	}
	return c.rs.concurrency
}

// pullSettings fetches the settings the visor holds for this app and installs
// them. It rides the keepalive loop's existing RTT tick — no new goroutine, no
// new timer, nothing to run on a wasm build with no app RPC. The visor answers
// nothing when its version matches the one we report as applied, so the steady
// state is one empty round-trip per tick.
//
// Reports whether anything changed, so the caller can re-cadence the tickers
// whose intervals are themselves knobs.
func (c *Client) pullSettings() bool {
	if c.appSettings == nil {
		return false
	}
	vals, version, err := c.appSettings(c.settingsApplied)
	if err != nil {
		if c.appCl != nil {
			c.appCl.Log().Debugf("Pulling app settings failed: %v", err)
		}
		return false
	}
	if version == c.settingsApplied {
		return false
	}
	changed := skysettings.Apply(vals)
	c.settingsApplied = version
	if changed && c.appCl != nil {
		c.appCl.Log().Infof("Applied %d live setting(s) at version %d", len(vals), version)
	}
	return changed
}

// livenessInterval is the liveness-probe cadence in force: the knob when it has
// been set, otherwise the interval NewClient snapshotted (livenessProbeInterval,
// which tests move before constructing a client).
func (c *Client) livenessInterval() time.Duration {
	if skysettings.IsSet(skysettings.TunnelLivenessInterval) {
		return setLivenessProbeInterval()
	}
	return c.probeInterval
}
