//go:build !gopus_qext

package encoder

func (e *Encoder) syncQEXTToCELT() {}

func (e *Encoder) maxOutputPacketBytes() int { return libopusMaxDataBytesCap * 6 }

func (e *Encoder) setCELTQEXTEnabled(bool) {}

func (e *Encoder) lastQEXTPayload() []byte {
	return nil
}
