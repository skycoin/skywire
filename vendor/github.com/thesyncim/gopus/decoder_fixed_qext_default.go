//go:build !gopus_fixed_point || !gopus_qext

package gopus

import (
	"github.com/thesyncim/gopus/internal/celt"
	"github.com/thesyncim/gopus/internal/rangecoding"
)

func (d *Decoder) decodeFixedQEXTCELTFrame(_ *rangecoding.Decoder, _, _ int, _ bool, _ celt.CELTBandwidth, _ []byte) (bool, error) {
	return false, nil
}

func (d *Decoder) decodeFixedQEXTCELTLostFrame(_ int) bool { return false }

func (d *Decoder) prepareFixedQEXTHybrid(_ []byte, _ celt.CELTBandwidth, _, _ bool, _ []byte) bool {
	return false
}

func (d *Decoder) decodeFixedQEXTHybridHighband(_ []int16, _ int, _ *rangecoding.Decoder, _, _, _ int, _ bool) bool {
	return false
}

func (d *Decoder) decodeFixedQEXTTransitionPLC(_ int) bool { return false }

func (d *Decoder) fixedAccumulateQEXTFECHybridToSILKFade(_, _ int, _ bool, _ celt.CELTBandwidth) bool {
	return false
}

func (d *Decoder) decodeFixedQEXTRedundantCELTWithChannels(_ []byte, _ celt.CELTBandwidth, _ bool, _ int) bool {
	return false
}

func (d *Decoder) armFixedQEXTHybridLost(_ int, _ bool) bool { return false }

func (d *Decoder) finishFixedQEXTHybridLost(_, _ int) bool { return false }

func (d *Decoder) decodeFixedQEXTHybridFEC(_ []float32, _, _ int, _ celt.CELTBandwidth) bool {
	return false
}

func (d *Decoder) fixedQEXTHybridFinalRange() uint32 { return d.mainDecodeRng }

func (d *Decoder) fixedQEXTRedundantFinalRange() (uint32, bool) { return 0, false }

func (d *Decoder) clearFixedQEXTHybrid() {}

func (d *Decoder) resetFixedQEXTCELT() {}

func (d *Decoder) invalidateFixedQEXTCELT() {}

func (d *Decoder) setFixedQEXTPhaseInversionDisabled(bool) {}
