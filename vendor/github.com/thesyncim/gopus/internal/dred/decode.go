package dred

import "github.com/thesyncim/gopus/internal/rangecoding"

// DRED latent geometry, mirroring libopus dnn/dred_rdovae_constants.h.
// StateDim is the RDOVAE initial-state vector width, LatentDim the per-frame
// latent width and LatentStride the stored stride (LatentDim plus the
// per-latent quantizer-level byte).
const (
	StateDim     = 50
	LatentDim    = 25
	LatentStride = LatentDim + 1
)

// Decoded retains the request-bounded DRED decoder state and latent vectors
// libopus produces during dred_ec_decode().
type Decoded struct {
	State     [StateDim]float32
	Latents   [MaxLatents * LatentStride]float32
	Features  [MaxFrames * NumFeatures]float32
	NbLatents int
}

// Clear resets the retained decoded DRED state.
func (d *Decoded) Clear() {
	if d == nil {
		return
	}
	*d = Decoded{}
}

// Invalidate drops retained decode visibility without zeroing the backing
// arrays, which keeps packet-entry invalidation cheap.
func (d *Decoded) Invalidate() {
	if d == nil {
		return
	}
	d.NbLatents = 0
}

// FillState copies the retained DRED state into dst and returns the number of
// floats written.
func (d *Decoded) FillState(dst []float32) int {
	if d == nil {
		return 0
	}
	n := min(len(d.State), len(dst))
	copy(dst[:n], d.State[:n])
	return n
}

// FillLatents copies the retained DRED latent vectors into dst and returns the
// number of floats written.
func (d *Decoded) FillLatents(dst []float32) int {
	if d == nil {
		return 0
	}
	n := min(d.NbLatents*LatentStride, len(dst))
	copy(dst[:n], d.Latents[:n])
	return n
}

// FillFeatures copies the retained DRED feature frames into dst and returns the
// number of floats written.
func (d *Decoded) FillFeatures(dst []float32) int {
	if d == nil {
		return 0
	}
	n := min(d.NbLatents*4*NumFeatures, len(dst))
	copy(dst[:n], d.Features[:n])
	return n
}

// Decode parses and retains the request-bounded DRED decoder state and latent
// vectors libopus produces during dred_ec_decode().
func (d *Decoded) Decode(payload []byte, dredFrameOffset, minFeatureFrames int) (Header, error) {
	if d == nil {
		return Header{}, errInvalidHeader
	}
	d.Clear()

	var rd rangecoding.Decoder
	header, err := parseHeaderWithDecoder(payload, dredFrameOffset, &rd)
	if err != nil {
		return Header{}, err
	}

	stateOffset := header.Q0 * StateDim
	decodeDREDLatentsInto(&rd, d.State[:], dredStateQuantScalesQ8[stateOffset:stateOffset+StateDim], dredStateRQ8[stateOffset:stateOffset+StateDim], dredStateP0Q8[stateOffset:stateOffset+StateDim])

	limit := NumRedundancyFrames
	requestLimit := (minFeatureFrames + 1) / 2
	if requestLimit < limit {
		limit = requestLimit
	}
	i := 0
	for ; i < limit; i += 2 {
		if len(payload)*8-rd.Tell() <= 7 {
			break
		}
		quant := header.QuantizerLevel(i / 2)
		offset := quant * LatentDim
		dst := d.Latents[(i/2)*LatentStride:]
		decodeDREDLatentsInto(&rd, dst[:LatentDim], dredLatentQuantScalesQ8[offset:offset+LatentDim], dredLatentRQ8[offset:offset+LatentDim], dredLatentP0Q8[offset:offset+LatentDim])
		dst[LatentDim] = float32(quant)*0.125 - 1
	}
	d.NbLatents = i / 2
	return header, nil
}

func decodeDREDLatentsInto(rd *rangecoding.Decoder, dst []float32, scale, rTable, p0Table []uint8) {
	for i := range dst {
		q := 0
		if rTable[i] != 0 && p0Table[i] != 255 {
			q = decodeLaplaceP0(rd, uint16(p0Table[i])<<7, uint16(rTable[i])<<7)
		}
		den := int(scale[i])
		if den == 0 {
			den = 1
		}
		dst[i] = float32(q*256) / float32(den)
	}
}
