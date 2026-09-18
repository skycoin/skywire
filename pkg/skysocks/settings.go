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

func setChunkProbeBytes() int64          { return skysettings.Bytes(skysettings.ChunkProbeBytes) }
func setChunkMinBytes() int64            { return skysettings.Bytes(skysettings.ChunkMinBytes) }
func setChunkPerTunnel() int             { return skysettings.Count(skysettings.ChunkPerTunnel) }
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

// The striped upload's knobs. The four the tests shrink (the stripe threshold,
// the chunk, the memory ceiling and the per-tunnel concurrency) are OVERRIDES
// in the same sense as the range-split pair: unset, the package var still wins,
// so a test that moves it is unaffected and a client that sets nothing uploads
// byte for byte as it does today. The rest read the knob directly, whose
// default is the constant they replaced.

func setUploadStripeMinBytes() int64 {
	if skysettings.IsSet(skysettings.UploadStripeMinBytes) {
		return skysettings.Bytes(skysettings.UploadStripeMinBytes)
	}
	return uploadStripeMinBytes
}

func setUploadChunkBytes() int64 {
	if skysettings.IsSet(skysettings.UploadChunkBytes) {
		return skysettings.Bytes(skysettings.UploadChunkBytes)
	}
	return uploadChunkBytes
}

func setUploadMemBytes() int64 {
	if skysettings.IsSet(skysettings.UploadMemBytes) {
		return skysettings.Bytes(skysettings.UploadMemBytes)
	}
	return uploadMemBytes
}

func setUploadConcurrency() int {
	if skysettings.IsSet(skysettings.UploadConcurrency) {
		return skysettings.Count(skysettings.UploadConcurrency)
	}
	return uploadConcurrency
}

func setUploadReplayMaxBytes() int64 {
	if skysettings.IsSet(skysettings.UploadReplayMaxBytes) {
		return skysettings.Bytes(skysettings.UploadReplayMaxBytes)
	}
	return uploadReplayMaxBytes
}

func setUploadProbeTTL() time.Duration { return skysettings.Dur(skysettings.UploadProbeTTL) }
func setUploadAckTimeout() time.Duration {
	return skysettings.Dur(skysettings.UploadAckTimeout)
}
func setUploadIdleTimeout() time.Duration {
	return skysettings.Dur(skysettings.UploadIdleTimeout)
}
func setUploadDurableWait() time.Duration {
	return skysettings.Dur(skysettings.UploadDurableWait)
}
func setUploadResendPasses() int { return skysettings.Count(skysettings.UploadResendPasses) }
func setUploadEarlyTries() int   { return skysettings.Count(skysettings.UploadEarlyTries) }
func setUploadEarlyWaitMax() time.Duration {
	return skysettings.Dur(skysettings.UploadEarlyWaitMax)
}
func setUploadBusyBackoff() time.Duration {
	return skysettings.Dur(skysettings.UploadBusyBackoff)
}
func setUploadBusyTries() int   { return skysettings.Count(skysettings.UploadBusyTries) }
func setUploadReplayTries() int { return skysettings.Count(skysettings.UploadReplayTries) }

// uploadTunables is ONE coherent read of the three knobs the slot arithmetic
// mixes. slots() divides the memory ceiling by the chunk size and subtracts a
// headroom counted in the same chunks, so each of those numbers has to come
// from the same instant: a settings pull landing between the two reads would
// otherwise divide the sink's window by a new chunk size and subtract a
// headroom measured in the old one, and the result is the over-subscription
// that makes the sink evict an already-acked chunk.
type uploadTunables struct {
	chunk       int64
	mem         int64
	concurrency int
}

func uploadSnapshot() uploadTunables {
	return uploadTunables{
		chunk:       setUploadChunkBytes(),
		mem:         setUploadMemBytes(),
		concurrency: setUploadConcurrency(),
	}
}

// perTunnel is how many chunks one tunnel may carry at once, floored at 1 so a
// knob set to zero cannot stall every upload.
func (t uploadTunables) perTunnel() int {
	if t.concurrency < 1 {
		return 1
	}
	return t.concurrency
}
