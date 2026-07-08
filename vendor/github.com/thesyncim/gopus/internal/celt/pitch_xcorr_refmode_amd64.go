//go:build amd64 && !purego

package celt

func libopusFloatPitchXCorrUsesAVX2FMA() bool {
	return amd64UsePitchXcorrAVX2FMA
}
