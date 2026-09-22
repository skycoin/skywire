// Package router pkg/router/settings_catalog_test.go
package router

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	rs "github.com/skycoin/skywire/pkg/router/routersettings"
	"github.com/skycoin/skywire/pkg/router/setupmetrics"
	"github.com/skycoin/skywire/pkg/routing"
)

// Every catalog default must be the constant the use site used to read. The
// catalog lives in its own package (so the CLI can parse values without linking
// pkg/router), which means the two can drift — this is the assertion that stops
// them. A visor that never calls `route settings` must behave byte for byte as
// it did.
func TestCatalogDefaultsMatchConstants(t *testing.T) {
	t.Cleanup(rs.Reset)

	ratios := []struct {
		k    *rs.Knob
		want float64
	}{
		{rs.BandDemoteRatio, bandDemoteRatioDefault},
		{rs.BandAdmitRatio, bandAdmitRatioDefault},
		{rs.BandDemoteRatioTight, bandDemoteRatioTightDefault},
		{rs.BandAdmitRatioTight, bandAdmitRatioTightDefault},
		{rs.BandGoodputGateFrac, goodputGateFracDefault},
		{rs.LegStarveRatio, legStarveRatioDefault},
		{rs.LegProbeMinBasisMs, legProbeMinBasisMsDefault},
		{rs.LegDelivAlpha, legDelivAlphaDefault},
		{rs.RackReorderFactor, rackReorderFactorDefault},
		{rs.EcfWindowMargin, ecfWindowMarginDefault},
		{rs.TLPPTOFactor, tlpPTOFactorDefault},
		{rs.HolRetxRTTFactor, holRetxRTTFactorDefault},
		{rs.SBDSkewTol, sbdSkewTolDefault},
		{rs.SBDCVTolFrac, sbdCVTolFracDefault},
		{rs.SBDFreqTol, sbdFreqTolDefault},
		{rs.SBDTrialLoss, sbdTrialLossDefault},
		{rs.EcfBeta, ecfBetaDefault},
		{rs.EcfRttAlpha, ecfRttAlphaDefault},
		{rs.EcfJitterAlpha, ecfJitterAlphaDefault},
		{rs.EcfCongestRttFactor, ecfCongestRttFactorDefault},
		{rs.EcfRttMinCreep, ecfRttMinCreepDefault},
		{rs.UnidirFlipRatio, flipRatioDefault},
		{rs.UnidirFanoutMaxSkew, forwardFanoutMaxSkewDefault},
		{rs.ForwardSwitchMargin, forwardSwitchMarginDefault},
		{rs.DialUnknownLatencyCostMs, dialUnknownLatencyCostDefaultMs},
		{rs.DialUnknownHopPenaltyMs, dialUnknownHopPenaltyDefaultMs},
		{rs.DialTypePriorScale, dialPriorScaleDefault},
		{rs.DialThroughputPriorScale, dialPriorScaleDefault},
	}
	for _, c := range ratios {
		require.Equal(t, c.want, c.k.Ratio(), c.k.Name())
	}

	durations := []struct {
		k    *rs.Knob
		want time.Duration
	}{
		{rs.LegLivenessInterval, legLivenessIntervalDefault},
		{rs.LegDataProgressInterval, legDataProgressIntervalDefault},
		{rs.LegDataStallGapAge, legDataStallGapAgeDefault},
		{rs.LegStateResyncInterval, legStateResyncIntervalDefault},
		{rs.LegParkMinHold, legParkMinHoldDefault},
		{rs.LegProbeMinWindow, legProbeMinWindowDefault},
		{rs.RackFloor, rackFloorDefault},
		{rs.RackCeil, rackCeilDefault},
		{rs.RackDefaultNoRTT, rackDefaultNoRTTDefault},
		{rs.RackRetxMinAge, retxMinAgeDefault},
		{rs.ReorderTimeout, reorderTimeoutDefault},
		{rs.ReorderStallInterval, reorderStallIntervalDefault},
		{rs.SendWindowWaitMax, sendWindowWaitMaxDefault},
		{rs.SendWindowPoll, sendWindowPollDefault},
		{rs.SendWindowRefreshInterval, windowRefreshIntervalDefault},
		{rs.SackMinInterval, sackMinIntervalDefault},
		{rs.SackDelayedAckDelay, delayedAckDelayDefault},
		{rs.TLPMinPTO, tlpMinPTODefault},
		{rs.TLPMaxPTO, tlpMaxPTODefault},
		{rs.TLPCheckInterval, tlpCheckIntervalDefault},
		{rs.HolRetxGapFloor, holRetxGapFloorDefault},
		{rs.HolRetxPerSeqFloor, holRetxPerSeqFloorDefault},
		{rs.SBDSampleInterval, sbdSampleIntervalDefault},
		{rs.SBDTrialWindow, sbdTrialWindowDefault},
		{rs.SBDBackoff, sbdBackoffDefault},
		{rs.UnidirFlipInterval, unidirFlipIntervalDefault},
		{rs.UnidirFanoutEngage, forwardFanoutEngageDefault},
		{rs.UnidirFanoutRelease, forwardFanoutReleaseDefault},
		{rs.DeadRouteHold, deadRouteTTLDefault},
		{rs.DeadRouteHoldMax, deadRouteMaxTTLDefault},
		{rs.DeadRouteYoungAge, deadRouteYoungAge},
		{rs.WarmPlanTTL, defaultWarmPlanTTL},
		{rs.ForwardWriteTimeout, forwardWriteTimeoutDefault},
		{rs.ForwardDropEventWindow, forwardDropEventWindowDefault},
		{rs.SetupBatchWindow, setupBatchWindowDefault},
		{rs.SetupPlanClaimTTL, setupPlanClaimTTLDefault},
		{rs.SetupCircuitOpenDuration, setupmetrics.CircuitOpenDurationDefault},
		{rs.SetupCircuitMaxOpenDuration, setupmetrics.CircuitMaxOpenDurationDefault},
		{rs.SetupCircuitFailWindow, setupmetrics.CircuitFailureWindowDefault},
	}
	for _, c := range durations {
		require.Equal(t, c.want, c.k.Duration(), c.k.Name())
	}

	counts := []struct {
		k    *rs.Knob
		want int64
	}{
		{rs.BandMinLegs, bandMinLegsDefault},
		{rs.LegPongMissThreshold, legPongMissThresholdDefault},
		{rs.LegBlackHoleMinTopBytes, legBlackHoleMinTopBytesDefault},
		{rs.LegSoleBlackHoleSentFloor, soleBlackHoleSentFloorDefault},
		{rs.LegSoleBlackHoleTicks, soleBlackHoleTicksDefault},
		{rs.LegProbeBytes, legProbeBytesDefault},
		{rs.RackDSACKGrowStep, rackDSACKGrowStepDefault},
		{rs.RackDecayStep, rackDecayStepDefault},
		{rs.RackRetxBackoffMaxShift, retxBackoffMaxShiftDefault},
		{rs.ReorderWindow, reorderWindowDefault},
		{rs.EcfMaxWindowBytes, ecfMaxWindowBytesDefault},
		{rs.EcfMinWindowBytes, ecfMinWindowBytesDefault},
		{rs.TLPMaxProbes, tlpMaxProbesDefault},
		{rs.HolRetxMaxFill, holRetxMaxFillDefault},
		{rs.SBDMinSamples, sbdMinSamplesDefault},
		{rs.SBDWindowSamples, sbdWindowSamplesDefault},
		{rs.SBDMinEvidenceRate, sbdMinEvidenceRateDefault},
		{rs.FECK, fecDefaultKDefault},
		{rs.FECR, fecDefaultRDefault},
		{rs.EcfDefaultFrameBytes, ecfDefaultFrameBytesDefault},
		{rs.EcfColdBootstrapBytes, ecfColdBootstrapBytesDefault},
		{rs.UnidirFlipHysteresis, flipHysteresisDefault},
		{rs.UnidirFlipCooldownTicks, flipCooldownTicksDefault},
		{rs.UnidirFlipMinGoodput, int64(flipMinGoodputDefault)},
		{rs.ForwardSwitchSamples, forwardSwitchSamplesDefault},
		{rs.DialCandidates, baseRouteCandidates},
		{rs.DialCandidateHeadroom, muxRouteHeadroom},
		{rs.DialForegroundMux, initialForegroundMux},
		{rs.DialTunnelLegs, dialTunnelLegsDefault},
		{rs.WarmPlanBucketCap, warmPlanBucketCap},
		{rs.SetupBatchMax, setupBatchMaxDefault},
		{rs.SetupFillInflight, setupFillInflightDefault},
		{rs.SetupFirstHopFilterMax, setupFirstHopFilterMaxDefault},
		{rs.SetupCircuitFailThreshold, setupmetrics.CircuitFailureThresholdDefault},
		{rs.MuxEventRingSize, MuxEventRingSizeDefault},
		{rs.MuxEventsPerGroup, muxEventsPerGroupDefault},
		{rs.ForwardQueueDepth, forwardQueueDepthDefault},
	}
	for _, c := range counts {
		require.EqualValues(t, c.want, c.k.Raw(), c.k.Name())
	}

	require.Equal(t, forwardSpillDefault, rs.ForwardSpill.Bool())
	require.Equal(t, sbdDemoteDefault, rs.SBDDemote.Bool())
	require.Equal(t, perFrameNoiseEnabledDefault, rs.MuxPerFrameNoise.Bool())
	require.Equal(t, setupmetrics.CircuitBreakerEnabledDefault, rs.SetupCircuitBreaker.Bool())
	require.EqualValues(t, rackFactorMaxDefault, (&routeMux{}).rackFactorMax())
	// The RACK adaptation's baseline is rack.reorder_factor in milli-units.
	require.EqualValues(t, rackReorderFactorDefault*1000, (&routeMux{}).rackFactorMin())
}

// The capability toggles are honored where a handshake is BUILT, so turning
// one off degrades NEW route groups and leaves groups already running alone.
func TestCapabilityTogglesAtNegotiation(t *testing.T) {
	t.Cleanup(rs.Reset)

	base := routing.CapMux | routing.CapSACK | routing.CapHOLRetx
	require.Equal(t, base, muxHandshakeCaps(), "everything is advertised by default")
	require.True(t, PerFrameNoiseEnabled())

	// HoL retransmit off: SACK still advertised, HoL is not.
	require.NoError(t, rs.Set(rs.MuxHOLRetx.Name(), "false"))
	got := muxHandshakeCaps()
	require.NotZero(t, got&routing.CapSACK)
	require.Zero(t, got&routing.CapHOLRetx)
	require.NotZero(t, got&routing.CapMux, "the mux capability itself is never masked")

	// SACK off implies HoL off — it reuses the SACK wire message, so it is
	// never advertised on its own.
	require.NoError(t, rs.Set(rs.MuxHOLRetx.Name(), "true"))
	require.NoError(t, rs.Set(rs.MuxSACK.Name(), "false"))
	got = muxHandshakeCaps()
	require.Zero(t, got&routing.CapSACK)
	require.Zero(t, got&routing.CapHOLRetx)
	require.False(t, HOLRetxAdvertised())

	// Per-frame noise is its own toggle, read when the group is served.
	require.NoError(t, rs.Set(rs.MuxPerFrameNoise.Name(), "false"))
	require.False(t, PerFrameNoiseEnabled())

	rs.Reset()
	require.Equal(t, base, muxHandshakeCaps(), "reset puts every capability back")
}

// A route group resolves its knobs against the app that owns it, and a group
// with no app tag keeps the visor-wide values. The resolution happens on the
// tag and on a change notification — never per packet.
func TestRouteGroupResolvesAppKnobs(t *testing.T) {
	t.Cleanup(rs.Reset)
	require.NoError(t, rs.Set(rs.LegStarveRatio.Name(), "4"))
	require.NoError(t, rs.SetApp("skysocks-client", rs.LegStarveRatio.Name(), "9"))

	subject := NewRouteGroup(DefaultRouteGroupConfig(), routing.NewTable(nil), routing.RouteDescriptor{}, nil)
	t.Cleanup(func() { _ = subject.Close() }) //nolint:errcheck
	subject.SetAppName("skysocks-client")

	reference := NewRouteGroup(DefaultRouteGroupConfig(), routing.NewTable(nil), routing.RouteDescriptor{}, nil)
	t.Cleanup(func() { _ = reference.Close() }) //nolint:errcheck
	reference.SetAppName("skysocks-client-ref")

	require.Equal(t, 9.0, subject.knRatio(rs.LegStarveRatio))
	require.Equal(t, 4.0, reference.knRatio(rs.LegStarveRatio),
		"the paired reference follows the visor-wide value")
	require.Equal(t, "skysocks-client", subject.knobs().App())

	// A later change to the visor-wide value reaches the reference on its next
	// refresh and leaves the subject's override alone.
	require.NoError(t, rs.Set(rs.LegStarveRatio.Name(), "5"))
	require.True(t, reference.refreshKnobs())
	require.Equal(t, 5.0, reference.knRatio(rs.LegStarveRatio))
	require.True(t, subject.refreshKnobs())
	require.Equal(t, 9.0, subject.knRatio(rs.LegStarveRatio))
}

// A group's own event ring holds its own history: a chatty sibling filling the
// router-wide ring can no longer evict what `mux info` reports for this group.
func TestPerGroupEventRingIsNotEvictedBySiblings(t *testing.T) {
	t.Cleanup(rs.Reset)
	require.NoError(t, rs.Set(rs.MuxEventRingSize.Name(), "8"))

	shared := &muxEventRing{}
	quiet := NewRouteGroup(DefaultRouteGroupConfig(), routing.NewTable(nil), routing.RouteDescriptor{}, nil)
	t.Cleanup(func() { _ = quiet.Close() }) //nolint:errcheck
	quiet.muxEvents = shared
	chatty := NewRouteGroup(DefaultRouteGroupConfig(), routing.NewTable(nil), routing.RouteDescriptor{}, nil)
	t.Cleanup(func() { _ = chatty.Close() }) //nolint:errcheck
	chatty.muxEvents = shared

	quiet.noteMuxEvent(MuxEvent{Event: MuxEventGroupCreated, Reason: "the one event that matters"})
	for i := 0; i < 64; i++ {
		chatty.noteMuxEvent(MuxEvent{Event: MuxEventLegParked, Reason: "churn"})
	}

	for _, e := range shared.snapshot() {
		require.NotEqual(t, "the one event that matters", e.Reason,
			"the shared ring has lost it to the sibling's churn, as it always would")
	}
	own := quiet.ownEvents.lastN(8)
	require.Len(t, own, 1)
	require.Equal(t, "the one event that matters", own[0].Reason)
}

// The four circuit-breaker parameters and the switch above them are live
// knobs, not constants: a set moves what the collector reads on its next
// call, with no restart and no route group rebuilt.
func TestSetupCircuitKnobsAreLive(t *testing.T) {
	t.Cleanup(rs.Reset)

	require.False(t, SetupCircuitBreaker(), "setup.circuit_breaker must ship off")
	require.True(t, SetSetupCircuitBreaker(true))
	require.True(t, SetupCircuitBreaker())

	require.True(t, SetSetupCircuitFailThreshold(7))
	require.Equal(t, 7, SetupCircuitFailThreshold())
	require.True(t, SetSetupCircuitOpenDuration(90*time.Second))
	require.Equal(t, 90*time.Second, SetupCircuitOpenDuration())
	require.True(t, SetSetupCircuitMaxOpenDuration(11*time.Minute))
	require.Equal(t, 11*time.Minute, SetupCircuitMaxOpenDuration())
	require.True(t, SetSetupCircuitFailWindow(45*time.Second))
	require.Equal(t, 45*time.Second, SetupCircuitFailWindow())

	// Nonsense is refused rather than installed, so a typo cannot disarm the
	// window bounds.
	require.False(t, SetSetupCircuitFailThreshold(0))
	require.Equal(t, 7, SetupCircuitFailThreshold())

	// And the catalog is where `route settings` finds them.
	seen := map[string]bool{}
	for _, e := range rs.Snapshot() {
		seen[e.Name] = true
	}
	for _, name := range []string{
		"setup.circuit_breaker", "setup.circuit_fail_threshold", "setup.circuit_open_duration",
		"setup.circuit_max_open_duration", "setup.circuit_fail_window",
	} {
		require.True(t, seen[name], name)
	}
}

// A reservation that failed at some hop now names that hop structurally, so
// setupmetrics can charge the failure to it instead of to the destination —
// while the message stays byte for byte what log greps and the setup node's
// own-view parser already match on.
func TestReserveErrorNamesTheFailedHop(t *testing.T) {
	pk, _ := cipher.GenerateKeyPair()
	inner := errors.New("connection is shut down")
	err := fmt.Errorf("failed to reserve route ids: %w", &ReserveError{PK: pk, Err: inner})

	require.Equal(t,
		"failed to reserve route ids: reserve routeID from "+pk.String()+" failed: connection is shut down",
		err.Error())
	require.True(t, errors.Is(err, inner))

	var re *ReserveError
	require.True(t, errors.As(err, &re))
	require.Equal(t, pk, re.DialFailedPK())
}
