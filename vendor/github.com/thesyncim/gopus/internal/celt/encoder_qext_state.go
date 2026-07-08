package celt

import "github.com/thesyncim/gopus/internal/rangecoding"

type encoderQEXTState struct {
	enabled     bool
	lastPayload []byte
}

type encoderQEXTScratch struct {
	buf       []byte
	extraBits []int32
	fineBits  []int32
	bandE     []celtEner
	bandLogE  []celtGLog
	quantized []celtGLog
	qerr      []celtGLog
	oldBandE  []celtGLog
	normL     []celtNorm
	normR     []celtNorm
	encoder   rangecoding.Encoder
}
