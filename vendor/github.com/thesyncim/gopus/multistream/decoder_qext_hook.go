//go:build gopus_qext

package multistream

func (d *streamState) setCELTQEXTPayload(payload []byte) {
	if d == nil || d.celtDec == nil {
		return
	}
	d.celtDec.SetQEXTPayload(payload)
}
