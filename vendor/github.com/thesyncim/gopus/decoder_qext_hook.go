//go:build gopus_qext

package gopus

func (d *Decoder) setCELTQEXTPayload(payload []byte) {
	if d == nil || d.celtDecoder == nil {
		return
	}
	d.celtDecoder.SetQEXTPayload(payload)
}
