//go:build !gopus_celt_trace || gopus_fixed_point

package celt

import "github.com/thesyncim/gopus/internal/rangecoding"

func beginTFEncodeTrace(*rangecoding.Encoder, int, int, bool, []int32, int, int) int { return -1 }

func tfEncodeTraceBit(_ int, re *rangecoding.Encoder, symbol int, logp uint, _ bool) {
	re.EncodeBit(symbol, logp)
}

func recordTFEncodeBudgeted(int, *rangecoding.Encoder, []int32) {}

func finishTFEncodeTrace(int, *rangecoding.Encoder, []int32, int) {}
