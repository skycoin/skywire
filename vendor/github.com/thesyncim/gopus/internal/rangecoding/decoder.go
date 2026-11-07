package rangecoding

import "math/bits"

var tellFracCorrection = [8]uint32{35733, 38967, 42495, 46340, 50535, 55109, 60097, 65535}

// Decoder implements the Opus range decoder (RFC 6716 Section 4.1), a bit-exact
// port of the libopus ec_dec context and the functions in celt/entdec.c. It is
// the exact symmetric inverse of [Encoder]: initializing a Decoder with the
// output of [Encoder.Done] and issuing the matching decode calls reproduces the
// original symbol sequence, with ec_tell staying in lockstep.
//
// Decoding a range-coded symbol is two steps, matching libopus: call
// [Decoder.Decode] (or DecodeBin) to read the cumulative frequency, locate the
// symbol in the model, then call [Decoder.Update] with that symbol's
// [fl, fh) interval. The DecodeICDF*, DecodeBit, and DecodeUniform helpers fuse
// both steps for the common cases. Raw bits are read from the end of the buffer
// with [Decoder.DecodeRawBits].
//
// The decoder is robust to truncated or malformed input: reads past the end of
// the buffer return zero bytes (per the RFC), so no method panics or indexes out
// of bounds on arbitrary input. Out-of-range coded values set the error flag
// reported by [Decoder.Error]. The field widths match libopus ec_ctx exactly.
type Decoder struct {
	buf        []byte // Input buffer
	storage    uint32 // Buffer size
	offs       uint32 // Current read offset
	endOffs    uint32 // End offset for raw bits
	endWindow  uint64 // Window for raw bits at end (wider than libopus' ec_window for fewer refills; same bit stream)
	nendBits   int32  // Number of valid bits in end window; libopus ec_ctx.nend_bits is C int.
	nbitsTotal int32  // Total bits read; libopus ec_ctx.nbits_total is C int.
	rng        uint32 // Range size (must stay > EC_CODE_BOT after normalize)
	val        uint32 // Current value in range
	ext        uint32 // Saved normalization factor from decode()
	rem        int32  // Buffered partial byte; libopus ec_ctx.rem is C int.
	err        int32  // Error flag; libopus ec_ctx.error is C int.
}

// Init initializes the decoder to read from buf, the bit-exact port of libopus
// ec_dec_init. It reads the first byte, seeds the range and value registers, and
// renormalizes so the decoder is ready for the first symbol. buf is treated as
// read-only and may be any length, including empty; reads past its end yield
// zero bytes per RFC 6716 Section 4.1.
func (d *Decoder) Init(buf []byte) {
	d.buf = buf
	d.storage = uint32(len(buf))
	d.offs = 0
	d.endOffs = 0
	d.endWindow = 0
	d.nendBits = 0
	d.err = 0

	// Initialize range to 1 << EC_CODE_EXTRA (128)
	d.rng = 1 << EC_CODE_EXTRA

	// Read first byte and compute initial value
	d.rem = int32(d.readByte())
	d.val = d.rng - 1 - uint32(d.rem>>(EC_SYM_BITS-EC_CODE_EXTRA))

	// Set initial bit count BEFORE normalize (matches libopus ec_dec_init).
	// This compensates for bits that will be added in normalize().
	d.nbitsTotal = int32(EC_CODE_BITS + 1 -
		((EC_CODE_BITS-EC_CODE_EXTRA)/EC_SYM_BITS)*EC_SYM_BITS)
	d.ext = 0

	// Normalize to fill the range (this will add more bits to nbitsTotal)
	d.normalize()
}

// readByte reads the next byte from the buffer.
// Returns 0 if reading past end (per spec).
//
//go:nosplit
func (d *Decoder) readByte() byte {
	offs := int(d.offs)
	if offs < len(d.buf) {
		b := d.buf[offs]
		d.offs++
		return b
	}
	return 0
}

// normalized is the ec_dec_normalize loop on the range and value a symbol
// decode leaves. Symbol decoders store its results with
// d.rng, d.val = d.normalized(rng, val): both stay in registers through the
// loop, so the next symbol does not wait on a store and reload of either.
//
//go:nosplit
func (d *Decoder) normalized(rng, val uint32) (uint32, uint32) {
	for rng <= EC_CODE_BOT {
		d.nbitsTotal += EC_SYM_BITS
		rng <<= EC_SYM_BITS
		sym := uint32(d.rem) << EC_SYM_BITS
		d.rem = int32(d.readByte())
		sym = (sym | uint32(d.rem)) >> (EC_SYM_BITS - EC_CODE_EXTRA)
		val = (val<<EC_SYM_BITS + EC_SYM_MAX&^sym) & (EC_CODE_TOP - 1)
	}
	return rng, val
}

// normalize ensures rng > EC_CODE_BOT by reading more bytes.
// This is the core renormalization loop from RFC 6716 Section 4.1.1.
//
//go:nosplit
func (d *Decoder) normalize() {
	for d.rng <= EC_CODE_BOT {
		d.nbitsTotal += int32(EC_SYM_BITS)
		d.rng <<= EC_SYM_BITS

		// Combine previous remainder with new byte
		sym := uint32(d.rem)
		d.rem = int32(d.readByte())
		sym = (sym<<EC_SYM_BITS | uint32(d.rem)) >> (EC_SYM_BITS - EC_CODE_EXTRA)

		// Update val: shift in new bits, mask to valid range
		d.val = ((d.val << EC_SYM_BITS) + (EC_SYM_MAX &^ sym)) & (EC_CODE_TOP - 1)
	}
}

// DecodeICDF decodes a symbol using an inverse cumulative distribution function table.
// The icdf table contains values in decreasing order from 256 down to 0.
// ftb is the number of bits of precision in the table (typically 8).
// Returns the decoded symbol index.
//
//go:nosplit
func (d *Decoder) DecodeICDF(icdf []uint8, ftb uint) int {
	_ = icdf[0]
	s := d.rng
	dval := d.val
	r := s >> ftb
	for ret, prob := range icdf {
		t := s
		s = r * uint32(prob)
		if dval >= s {
			d.rng, d.val = d.normalized(t-s, dval-s)
			return ret
		}
	}
	return len(icdf) - 1
}

// DecodeICDF8 decodes a symbol using an 8-bit ICDF table.
// This is the hot SILK/CELT entropy path and matches DecodeICDF(icdf, 8).
//
//go:nosplit
func (d *Decoder) DecodeICDF8(icdf []uint8) int {
	_ = icdf[0]
	return d.DecodeICDF8Unchecked(icdf)
}

// DecodeICDF8Unchecked decodes using a non-empty 8-bit ICDF table.
// Codec hot paths pass static tables. Callers must not pass an empty slice.
//
//go:nosplit
func (d *Decoder) DecodeICDF8Unchecked(icdf []uint8) int {
	_ = icdf[0]
	switch len(icdf) {
	case 2:
		return d.DecodeICDF2_8(icdf[0])
	case 3:
		return d.DecodeICDF3_8(icdf[0], icdf[1])
	case 4:
		return d.DecodeICDF4_8(icdf[0], icdf[1], icdf[2])
	case 5:
		return d.DecodeICDF5_8(icdf[0], icdf[1], icdf[2], icdf[3])
	case 6:
		r := d.rng >> 8
		t := d.rng
		dval := d.val
		s := r * uint32(icdf[0])
		if dval >= s {
			d.rng, d.val = d.normalized(t-s, dval-s)
			return 0
		}
		t = s
		s = r * uint32(icdf[1])
		if dval >= s {
			d.rng, d.val = d.normalized(t-s, dval-s)
			return 1
		}
		t = s
		s = r * uint32(icdf[2])
		if dval >= s {
			d.rng, d.val = d.normalized(t-s, dval-s)
			return 2
		}
		t = s
		s = r * uint32(icdf[3])
		if dval >= s {
			d.rng, d.val = d.normalized(t-s, dval-s)
			return 3
		}
		t = s
		s = r * uint32(icdf[4])
		if dval >= s {
			d.rng, d.val = d.normalized(t-s, dval-s)
			return 4
		}
		d.rng, d.val = d.normalized(s, dval)
		return 5
	case 8:
		r := d.rng >> 8
		t := d.rng
		dval := d.val
		s := r * uint32(icdf[0])
		if dval >= s {
			d.rng, d.val = d.normalized(t-s, dval-s)
			return 0
		}
		t = s
		s = r * uint32(icdf[1])
		if dval >= s {
			d.rng, d.val = d.normalized(t-s, dval-s)
			return 1
		}
		t = s
		s = r * uint32(icdf[2])
		if dval >= s {
			d.rng, d.val = d.normalized(t-s, dval-s)
			return 2
		}
		t = s
		s = r * uint32(icdf[3])
		if dval >= s {
			d.rng, d.val = d.normalized(t-s, dval-s)
			return 3
		}
		t = s
		s = r * uint32(icdf[4])
		if dval >= s {
			d.rng, d.val = d.normalized(t-s, dval-s)
			return 4
		}
		t = s
		s = r * uint32(icdf[5])
		if dval >= s {
			d.rng, d.val = d.normalized(t-s, dval-s)
			return 5
		}
		t = s
		s = r * uint32(icdf[6])
		if dval >= s {
			d.rng, d.val = d.normalized(t-s, dval-s)
			return 6
		}
		d.rng, d.val = d.normalized(s, dval)
		return 7
	}
	s := d.rng
	dval := d.val
	r := s >> 8
	last := len(icdf) - 1
	ret := 0
	for ; ret+1 < last; ret += 2 {
		t := s
		s = r * uint32(icdf[ret])
		if dval >= s {
			d.rng, d.val = d.normalized(t-s, dval-s)
			return ret
		}
		t = s
		s = r * uint32(icdf[ret+1])
		if dval >= s {
			d.rng, d.val = d.normalized(t-s, dval-s)
			return ret + 1
		}
	}
	for ; ret < last; ret++ {
		t := s
		s = r * uint32(icdf[ret])
		if dval >= s {
			d.rng, d.val = d.normalized(t-s, dval-s)
			return ret
		}
	}
	d.rng, d.val = d.normalized(s, dval)
	return last
}

// DecodeICDF8Linear decodes a non-empty 8-bit ICDF table with the generic
// linear walk directly. It is for known large tables, where DecodeICDF8Unchecked
// would first pay the small-table switch before reaching this same loop.
//
//go:nosplit
func (d *Decoder) DecodeICDF8Linear(icdf []uint8) int {
	_ = icdf[0]
	s := d.rng
	dval := d.val
	r := s >> 8
	last := len(icdf) - 1
	for ret := range last {
		t := s
		s = r * uint32(icdf[ret])
		if dval >= s {
			d.rng, d.val = d.normalized(t-s, dval-s)
			return ret
		}
	}
	d.rng, d.val = d.normalized(s, dval)
	return last
}

// DecodeICDF8UncheckedN decodes using the first n entries of a non-empty
// 8-bit ICDF table. It is useful for packed zero-terminated table rows where
// the backing slice continues with subsequent rows.
//
//go:nosplit
func (d *Decoder) DecodeICDF8UncheckedN(icdf []uint8, n int) int {
	_ = icdf[n-1]
	s := d.rng
	dval := d.val
	r := s >> 8
	last := n - 1
	for ret := range last {
		t := s
		s = r * uint32(icdf[ret])
		if dval >= s {
			d.rng, d.val = d.normalized(t-s, dval-s)
			return ret
		}
	}
	d.rng, d.val = d.normalized(s, dval)
	return last
}

// DecodeICDF8UncheckedNOffset decodes using n entries starting at off in a
// non-empty 8-bit ICDF table. This avoids materializing row slices in hot paths
// backed by packed static table storage.
//
//go:nosplit
func (d *Decoder) DecodeICDF8UncheckedNOffset(icdf []uint8, off, n int) int {
	_ = icdf[off+n-1]
	row := icdf[off : off+n : off+n]
	s := d.rng
	dval := d.val
	r := s >> 8
	last := n - 1
	for ret, prob := range row[:last] {
		t := s
		s = r * uint32(prob)
		if dval >= s {
			d.rng, d.val = d.normalized(t-s, dval-s)
			return ret
		}
	}
	d.rng, d.val = d.normalized(s, dval)
	return last
}

// DecodeICDF2 decodes a 2-symbol ICDF with entries [icdf0, 0].
// This avoids generic loop/slice overhead in hot binary-symbol call sites.
//
//go:nosplit
func (d *Decoder) DecodeICDF2(icdf0 uint8, ftb uint) int {
	t := d.rng
	dval := d.val
	r := t >> ftb
	s := r * uint32(icdf0)
	if dval >= s {
		d.rng, d.val = d.normalized(t-s, dval-s)
		return 0
	}
	d.rng, d.val = d.normalized(s, dval)
	return 1
}

// DecodeICDF2_8 decodes a 2-symbol 8-bit ICDF with entries [icdf0, 0].
//
//go:nosplit
func (d *Decoder) DecodeICDF2_8(icdf0 uint8) int {
	rng := d.rng
	val := d.val

	t := rng
	r := t >> 8
	s := r * uint32(icdf0)
	ret := 1
	if val >= s {
		val -= s
		rng = t - s
		ret = 0
	} else {
		rng = s
	}
	if rng > EC_CODE_BOT {
		d.rng = rng
		d.val = val
		return ret
	}
	buf := d.buf
	offs := d.offs
	nbitsTotal := d.nbitsTotal
	rem := d.rem
	for {
		nbitsTotal += EC_SYM_BITS
		rng <<= EC_SYM_BITS

		sym := uint32(rem)
		if int(offs) < len(buf) {
			rem = int32(buf[offs])
			offs++
		} else {
			rem = 0
		}
		sym = (sym<<EC_SYM_BITS | uint32(rem)) >> (EC_SYM_BITS - EC_CODE_EXTRA)
		val = ((val << EC_SYM_BITS) + (EC_SYM_MAX &^ sym)) & (EC_CODE_TOP - 1)
		if rng > EC_CODE_BOT {
			break
		}
	}
	d.offs = offs
	d.nbitsTotal = nbitsTotal
	d.rng = rng
	d.val = val
	d.rem = rem
	return ret
}

// DecodeICDF3_8 decodes a 3-symbol 8-bit ICDF with entries [icdf0, icdf1, 0].
//
//go:nosplit
func (d *Decoder) DecodeICDF3_8(icdf0, icdf1 uint8) int {
	rng := d.rng
	val := d.val

	r := rng >> 8
	t := rng
	s := r * uint32(icdf0)
	ret := 2
	if val >= s {
		val -= s
		rng = t - s
		ret = 0
	} else {
		t = s
		s = r * uint32(icdf1)
		if val >= s {
			val -= s
			rng = t - s
			ret = 1
		} else {
			rng = s
		}
	}
	if rng > EC_CODE_BOT {
		d.rng = rng
		d.val = val
		return ret
	}
	buf := d.buf
	offs := d.offs
	nbitsTotal := d.nbitsTotal
	rem := d.rem
	for {
		nbitsTotal += EC_SYM_BITS
		rng <<= EC_SYM_BITS

		sym := uint32(rem)
		if int(offs) < len(buf) {
			rem = int32(buf[offs])
			offs++
		} else {
			rem = 0
		}
		sym = (sym<<EC_SYM_BITS | uint32(rem)) >> (EC_SYM_BITS - EC_CODE_EXTRA)
		val = ((val << EC_SYM_BITS) + (EC_SYM_MAX &^ sym)) & (EC_CODE_TOP - 1)
		if rng > EC_CODE_BOT {
			break
		}
	}
	d.offs = offs
	d.nbitsTotal = nbitsTotal
	d.rng = rng
	d.val = val
	d.rem = rem
	return ret
}

// DecodeICDF4_8 decodes a 4-symbol 8-bit ICDF with entries [icdf0, icdf1, icdf2, 0].
//
//go:nosplit
func (d *Decoder) DecodeICDF4_8(icdf0, icdf1, icdf2 uint8) int {
	rng := d.rng
	val := d.val

	r := rng >> 8
	t := rng
	s := r * uint32(icdf0)
	ret := 3
	if val >= s {
		val -= s
		rng = t - s
		ret = 0
	} else {
		t = s
		s = r * uint32(icdf1)
		if val >= s {
			val -= s
			rng = t - s
			ret = 1
		} else {
			t = s
			s = r * uint32(icdf2)
			if val >= s {
				val -= s
				rng = t - s
				ret = 2
			} else {
				rng = s
			}
		}
	}
	if rng > EC_CODE_BOT {
		d.rng = rng
		d.val = val
		return ret
	}
	buf := d.buf
	offs := d.offs
	nbitsTotal := d.nbitsTotal
	rem := d.rem
	for {
		nbitsTotal += EC_SYM_BITS
		rng <<= EC_SYM_BITS

		sym := uint32(rem)
		if int(offs) < len(buf) {
			rem = int32(buf[offs])
			offs++
		} else {
			rem = 0
		}
		sym = (sym<<EC_SYM_BITS | uint32(rem)) >> (EC_SYM_BITS - EC_CODE_EXTRA)
		val = ((val << EC_SYM_BITS) + (EC_SYM_MAX &^ sym)) & (EC_CODE_TOP - 1)
		if rng > EC_CODE_BOT {
			break
		}
	}
	d.offs = offs
	d.nbitsTotal = nbitsTotal
	d.rng = rng
	d.val = val
	d.rem = rem
	return ret
}

// DecodeICDF5_8 decodes a 5-symbol 8-bit ICDF with entries [icdf0, icdf1, icdf2, icdf3, 0].
//
//go:nosplit
func (d *Decoder) DecodeICDF5_8(icdf0, icdf1, icdf2, icdf3 uint8) int {
	rng := d.rng
	val := d.val

	r := rng >> 8
	t := rng
	s := r * uint32(icdf0)
	ret := 4
	if val >= s {
		val -= s
		rng = t - s
		ret = 0
	} else {
		t = s
		s = r * uint32(icdf1)
		if val >= s {
			val -= s
			rng = t - s
			ret = 1
		} else {
			t = s
			s = r * uint32(icdf2)
			if val >= s {
				val -= s
				rng = t - s
				ret = 2
			} else {
				t = s
				s = r * uint32(icdf3)
				if val >= s {
					val -= s
					rng = t - s
					ret = 3
				} else {
					rng = s
				}
			}
		}
	}
	if rng > EC_CODE_BOT {
		d.rng = rng
		d.val = val
		return ret
	}
	buf := d.buf
	offs := d.offs
	nbitsTotal := d.nbitsTotal
	rem := d.rem
	for {
		nbitsTotal += EC_SYM_BITS
		rng <<= EC_SYM_BITS

		sym := uint32(rem)
		if int(offs) < len(buf) {
			rem = int32(buf[offs])
			offs++
		} else {
			rem = 0
		}
		sym = (sym<<EC_SYM_BITS | uint32(rem)) >> (EC_SYM_BITS - EC_CODE_EXTRA)
		val = ((val << EC_SYM_BITS) + (EC_SYM_MAX &^ sym)) & (EC_CODE_TOP - 1)
		if rng > EC_CODE_BOT {
			break
		}
	}
	d.offs = offs
	d.nbitsTotal = nbitsTotal
	d.rng = rng
	d.val = val
	d.rem = rem
	return ret
}

// DecodeICDF7_8Slice decodes a 7-symbol 8-bit ICDF.
//
//go:nosplit
func (d *Decoder) DecodeICDF7_8Slice(icdf []uint8) int {
	_ = icdf[5]
	r := d.rng >> 8
	t := d.rng
	dval := d.val
	s := r * uint32(icdf[0])
	if dval >= s {
		d.rng, d.val = d.normalized(t-s, dval-s)
		return 0
	}
	t = s
	s = r * uint32(icdf[1])
	if dval >= s {
		d.rng, d.val = d.normalized(t-s, dval-s)
		return 1
	}
	t = s
	s = r * uint32(icdf[2])
	if dval >= s {
		d.rng, d.val = d.normalized(t-s, dval-s)
		return 2
	}
	t = s
	s = r * uint32(icdf[3])
	if dval >= s {
		d.rng, d.val = d.normalized(t-s, dval-s)
		return 3
	}
	t = s
	s = r * uint32(icdf[4])
	if dval >= s {
		d.rng, d.val = d.normalized(t-s, dval-s)
		return 4
	}
	t = s
	s = r * uint32(icdf[5])
	if dval >= s {
		d.rng, d.val = d.normalized(t-s, dval-s)
		return 5
	}
	d.rng, d.val = d.normalized(s, dval)
	return 6
}

// DecodeICDF9_8Slice decodes a 9-symbol 8-bit ICDF.
//
//go:nosplit
func (d *Decoder) DecodeICDF9_8Slice(icdf []uint8) int {
	_ = icdf[7]
	r := d.rng >> 8
	t := d.rng
	dval := d.val
	s := r * uint32(icdf[0])
	if dval >= s {
		d.rng, d.val = d.normalized(t-s, dval-s)
		return 0
	}
	t = s
	s = r * uint32(icdf[1])
	if dval >= s {
		d.rng, d.val = d.normalized(t-s, dval-s)
		return 1
	}
	t = s
	s = r * uint32(icdf[2])
	if dval >= s {
		d.rng, d.val = d.normalized(t-s, dval-s)
		return 2
	}
	t = s
	s = r * uint32(icdf[3])
	if dval >= s {
		d.rng, d.val = d.normalized(t-s, dval-s)
		return 3
	}
	t = s
	s = r * uint32(icdf[4])
	if dval >= s {
		d.rng, d.val = d.normalized(t-s, dval-s)
		return 4
	}
	t = s
	s = r * uint32(icdf[5])
	if dval >= s {
		d.rng, d.val = d.normalized(t-s, dval-s)
		return 5
	}
	t = s
	s = r * uint32(icdf[6])
	if dval >= s {
		d.rng, d.val = d.normalized(t-s, dval-s)
		return 6
	}
	t = s
	s = r * uint32(icdf[7])
	if dval >= s {
		d.rng, d.val = d.normalized(t-s, dval-s)
		return 7
	}
	d.rng, d.val = d.normalized(s, dval)
	return 8
}

// DecodeICDF2_8SignBlock16 decodes the sign of every positive entry of one
// 16-sample SILK shell block, in order, with the binary 8-bit iCDF
// {icdf0, 0}. Symbol 0 negates the entry and symbol 1 keeps it, which is
// libopus silk/decode_pulses.c silk_decode_signs:
// q[j] *= silk_dec_map(ec_dec_icdf(dec, icdf, 8)).
//
// Every entry must be non-negative, as the decoded pulse magnitudes are. The
// loop visits only the positive entries, found from a bit mask built four
// entries at a time, and updates the range state with masks instead of
// branching on each sign, whose outcome is unpredictable.
func (d *Decoder) DecodeICDF2_8SignBlock16(icdf0 uint8, block *[16]int16) {
	nonzero := positiveMask4(block[0], block[1], block[2], block[3]) |
		positiveMask4(block[4], block[5], block[6], block[7])<<4 |
		positiveMask4(block[8], block[9], block[10], block[11])<<8 |
		positiveMask4(block[12], block[13], block[14], block[15])<<12
	if nonzero == 0 {
		return
	}
	icdf := uint32(icdf0)
	buf := d.buf
	offs := d.offs
	nbitsTotal := d.nbitsTotal
	rng := d.rng
	val := d.val
	rem := d.rem
	for nonzero != 0 {
		j := bits.TrailingZeros32(nonzero) & 15
		nonzero &= nonzero - 1
		s := (rng >> 8) * icdf
		// neg is all ones when val >= s, which decodes symbol 0.
		var neg uint32
		if val >= s {
			neg = ^uint32(0)
		}
		val -= s & neg
		rng = (rng-s)&neg | s&^neg
		v := block[j]
		block[j] = (v ^ int16(neg)) - int16(neg)
		for rng <= EC_CODE_BOT {
			nbitsTotal += EC_SYM_BITS
			rng <<= EC_SYM_BITS

			sym := uint32(rem)
			if int(offs) < len(buf) {
				rem = int32(buf[offs])
				offs++
			} else {
				rem = 0
			}
			sym = (sym<<EC_SYM_BITS | uint32(rem)) >> (EC_SYM_BITS - EC_CODE_EXTRA)
			val = ((val << EC_SYM_BITS) + (EC_SYM_MAX &^ sym)) & (EC_CODE_TOP - 1)
		}
	}
	d.offs = offs
	d.nbitsTotal = nbitsTotal
	d.rng = rng
	d.val = val
	d.rem = rem
}

// positiveMask4 returns a 4-bit mask with bit j set when the j-th of four
// non-negative pulse magnitudes is positive. The magnitudes are packed into
// 16-bit lanes; adding 0x7fff to a lane sets its top bit exactly when the lane
// is non-zero and never carries into the next lane, and one multiply gathers
// the four top bits into adjacent positions.
func positiveMask4(a, b, c, d int16) uint32 {
	w := uint64(uint16(a)) | uint64(uint16(b))<<16 | uint64(uint16(c))<<32 | uint64(uint16(d))<<48
	top := ((w + 0x7fff7fff7fff7fff) & 0x8000800080008000) >> 15
	return uint32((top*0x0000200040008001)>>45) & 0xf
}

// DecodeICDF16 decodes a symbol using a uint16 ICDF table.
// This variant is needed because SILK ICDF tables use values 0-256,
// and 256 doesn't fit in uint8.
// The icdf table contains values in decreasing order from 256 down to 0.
// ftb is the number of bits of precision in the table (typically 8).
// Returns the decoded symbol index.
//
//go:nosplit
func (d *Decoder) DecodeICDF16(icdf []uint16, ftb uint) int {
	_ = icdf[0]
	s := d.rng
	dval := d.val
	r := s >> ftb
	for ret, prob := range icdf {
		t := s
		s = r * uint32(prob)
		if dval >= s {
			d.rng, d.val = d.normalized(t-s, dval-s)
			return ret
		}
	}
	return len(icdf) - 1
}

// DecodeICDF16_8 decodes a symbol using a uint16 ICDF table with 8-bit precision.
//
//go:nosplit
func (d *Decoder) DecodeICDF16_8(icdf []uint16) int {
	_ = icdf[0]
	return d.DecodeICDF16_8Unchecked(icdf)
}

// DecodeICDF16_8Unchecked decodes using a non-empty uint16 ICDF table with
// 8-bit precision. Callers must not pass an empty slice.
//
//go:nosplit
func (d *Decoder) DecodeICDF16_8Unchecked(icdf []uint16) int {
	_ = icdf[0]
	s := d.rng
	dval := d.val
	r := s >> 8
	for ret, prob := range icdf {
		t := s
		s = r * uint32(prob)
		if dval >= s {
			d.rng, d.val = d.normalized(t-s, dval-s)
			return ret
		}
	}
	return len(icdf) - 1
}

// DecodeBit decodes a single bit with the given log probability.
// logp is the number of bits of probability for a 0 (1 to 15).
// P(0) = 1 - 1/(2^logp), P(1) = 1/(2^logp)
// Returns 0 or 1.
//
// Per libopus entdec.c, the probability regions are:
// - [0, s): bit = 1, probability = 1 / 2^logp (rare, bottom region)
// - [s, rng): bit = 0, probability = (2^logp - 1) / 2^logp
//
// For silence flag (logp=15): P(silence=1) = 1/32768, which is very rare.
//
//go:nosplit
func (d *Decoder) DecodeBit(logp uint) int {
	r := d.rng
	dval := d.val
	// logp is at most 15, so the mask only lets the shift compile without
	// Go's out-of-range shift handling.
	s := r >> (logp & 31)

	// Per libopus: bit is 1 when dval < s (bottom region). one is all ones
	// then, and the updates select with it instead of branching.
	var one uint32
	if dval < s {
		one = ^uint32(0)
	}
	d.rng, d.val = d.normalized(s&one|(r-s)&^one, dval-s&^one)
	return int(one & 1)
}

// SkipToTell advances the decoder's bit accounting so Tell() reports exactly
// targetBits, without consuming further input. It mirrors the CELT silence
// handling in celt_decode_with_ec: dec->nbits_total += tell - ec_tell(dec).
func (d *Decoder) SkipToTell(targetBits int) {
	d.nbitsTotal += int32(targetBits - d.Tell())
}

// Tell returns the number of bits consumed from the combined stream so far,
// rounded up to the nearest whole bit. This is the libopus ec_tell macro
// (nbits_total - EC_ILOG(rng)) and counts both range-coded symbols and raw bits.
// It stays in lockstep with [Encoder.Tell] on the encoding side.
func (d *Decoder) Tell() int {
	return int(d.nbitsTotal) - ilog(d.rng)
}

// TellFrac returns the number of bits consumed from the combined stream so far
// in 1/8th-bit (BITRES) precision; divide by 8 to compare with [Decoder.Tell].
// It is the bit-exact port of libopus ec_tell_frac and matches [Encoder.TellFrac].
func (d *Decoder) TellFrac() int {
	nbits := int(d.nbitsTotal) << 3
	l := ilog(d.rng)
	r := d.rng >> (l - 16)
	b := (r >> 12) - 8
	// b += r > correction[b], without a branch: both are below 1<<16.
	b += (tellFracCorrection[b&7] - r) >> 31
	return nbits - ((l << 3) + int(b))
}

// State returns the internal range decoder state (rng, val).
// Useful for bit-exact comparisons against libopus in tests.
func (d *Decoder) State() (uint32, uint32) {
	return d.rng, d.val
}

// ilog returns the index of the most significant set bit plus one (so
// ilog(0)==0, ilog(1)==1, ilog(0x80000000)==32). It is the EC_ILOG primitive
// used by ec_tell and the renormalization/finalization math.
func ilog(x uint32) int {
	return bits.Len32(x)
}

// Error returns the decoder error flag (libopus ec_ctx.error). A non-zero value
// means malformed input was detected, currently an out-of-range value reported
// by [Decoder.DecodeUniform]. Note that, per the RFC, reading past the end of a
// truncated buffer is not an error: it yields zero bytes.
func (d *Decoder) Error() int {
	return int(d.err)
}

// BytesUsed returns the number of bytes consumed from the buffer.
func (d *Decoder) BytesUsed() int {
	return int(d.offs)
}

// StorageBits returns the total number of bits in the input buffer.
func (d *Decoder) StorageBits() int {
	return int(d.storage * 8)
}

// ShrinkStorage reduces the effective input size by the given number of bytes.
// This is used to exclude trailing redundancy bytes from the range decoder
// while preserving the current decoding state. The raw-bits window may have
// read ahead past what was consumed, so whole unconsumed bytes are first
// returned to the pool — making the observable state identical to a reader
// that refills one byte at a time — before the end offset is rebased onto
// the shrunk buffer.
func (d *Decoder) ShrinkStorage(bytes int) {
	if bytes <= 0 {
		return
	}
	if back := uint32(d.nendBits) >> 3; back > 0 {
		d.endOffs -= back
		d.nendBits -= int32(back << 3)
		d.endWindow &= 1<<uint(d.nendBits) - 1
	}
	if uint32(bytes) >= d.storage {
		d.storage = 0
		d.buf = d.buf[:0]
		if d.offs > d.storage {
			d.offs = d.storage
		}
		if d.endOffs > d.storage {
			d.endOffs = d.storage
		}
		return
	}
	d.storage -= uint32(bytes)
	d.buf = d.buf[:int(d.storage)]
	if d.offs > d.storage {
		d.offs = d.storage
	}
	if d.endOffs > d.storage {
		d.endOffs = d.storage
	}
}

// Range returns the current range value.
func (d *Decoder) Range() uint32 {
	return d.rng
}

// Val returns the current val.
func (d *Decoder) Val() uint32 {
	return d.val
}

// Offs returns the current read offset.
func (d *Decoder) Offs() uint32 {
	return d.offs
}

// DecodeUniform decodes a uniformly distributed integer in [0, ft), the
// bit-exact port of libopus ec_dec_uint and the inverse of
// [Encoder.EncodeUniform]. For ft larger than 1<<EC_UINT_BITS the high
// EC_UINT_BITS bits come from the range coder and the low bits from the raw-bit
// stream. If the reconstructed value exceeds ft-1 (only possible on malformed
// input) the error flag is set and ft-1 is returned. Used for fine energy bits,
// PVQ indices, and similar fields.
//
//go:nosplit
func (d *Decoder) DecodeUniform(ft uint32) uint32 {
	if ft <= 1 {
		return 0
	}
	ft--
	ftb := ilog(ft)

	if ftb > EC_UINT_BITS {
		ftb -= EC_UINT_BITS
		ft1 := (ft >> uint(ftb)) + 1
		rng := d.rng
		val := d.val
		ext := rng / ft1
		s := val / ext
		if s+1 > ft1 {
			s = ft1 - 1
		}
		ret := ft1 - (s + 1)
		scale := ext * s
		val -= scale
		if ret > 0 {
			rng = ext
		} else {
			rng -= scale
		}
		if rng > EC_CODE_BOT {
			d.rng = rng
			d.val = val
		} else {
			buf := d.buf
			offs := d.offs
			nbitsTotal := d.nbitsTotal
			rem := d.rem
			for {
				nbitsTotal += EC_SYM_BITS
				rng <<= EC_SYM_BITS

				sym := uint32(rem)
				if int(offs) < len(buf) {
					rem = int32(buf[offs])
					offs++
				} else {
					rem = 0
				}
				sym = (sym<<EC_SYM_BITS | uint32(rem)) >> (EC_SYM_BITS - EC_CODE_EXTRA)
				val = ((val << EC_SYM_BITS) + (EC_SYM_MAX &^ sym)) & (EC_CODE_TOP - 1)
				if rng > EC_CODE_BOT {
					break
				}
			}
			d.offs = offs
			d.nbitsTotal = nbitsTotal
			d.rng = rng
			d.val = val
			d.rem = rem
		}

		rawBits := uint(ftb)
		endWindow := d.endWindow
		nendBits := int(d.nendBits)
		if nendBits < ftb {
			endOffs := d.endOffs
			storage := d.storage
			buf := d.buf
			for nendBits <= 56 && endOffs < storage {
				endOffs++
				endWindow |= uint64(buf[storage-endOffs]) << uint(nendBits)
				nendBits += 8
			}
			if nendBits < ftb {
				nendBits = ftb
			}
			d.endOffs = endOffs
		}
		raw := uint32(endWindow) & ((1 << rawBits) - 1)
		d.endWindow = endWindow >> rawBits
		d.nendBits = int32(nendBits - ftb)
		d.nbitsTotal += int32(ftb)

		t := (ret << rawBits) | raw
		if t <= ft {
			return t
		}
		d.err = 1
		return ft
	}

	ft++
	rng := d.rng
	val := d.val
	ext := rng / ft
	s := val / ext
	if s+1 > ft {
		s = ft - 1
	}
	ret := ft - (s + 1)
	scale := ext * s
	val -= scale
	if ret > 0 {
		rng = ext
	} else {
		rng -= scale
	}
	if rng > EC_CODE_BOT {
		d.rng = rng
		d.val = val
		return ret
	}
	buf := d.buf
	offs := d.offs
	nbitsTotal := d.nbitsTotal
	rem := d.rem
	for {
		nbitsTotal += EC_SYM_BITS
		rng <<= EC_SYM_BITS

		sym := uint32(rem)
		if int(offs) < len(buf) {
			rem = int32(buf[offs])
			offs++
		} else {
			rem = 0
		}
		sym = (sym<<EC_SYM_BITS | uint32(rem)) >> (EC_SYM_BITS - EC_CODE_EXTRA)
		val = ((val << EC_SYM_BITS) + (EC_SYM_MAX &^ sym)) & (EC_CODE_TOP - 1)
		if rng > EC_CODE_BOT {
			break
		}
	}
	d.offs = offs
	d.nbitsTotal = nbitsTotal
	d.rng = rng
	d.val = val
	d.rem = rem
	return ret
}

// DecodeUniformSmall decodes a uniform value for totals that fit entirely in
// the range coder path. Callers must only use this for ft <= 1<<EC_UINT_BITS.
//
//go:nosplit
func (d *Decoder) DecodeUniformSmall(ft uint32) uint32 {
	if ft <= 1 {
		return 0
	}
	rng := d.rng
	val := d.val
	ext := rng / ft
	s := val / ext
	if s+1 > ft {
		s = ft - 1
	}
	ret := ft - (s + 1)
	scale := ext * s
	val -= scale
	if ret > 0 {
		rng = ext
	} else {
		rng -= scale
	}
	if rng > EC_CODE_BOT {
		d.rng = rng
		d.val = val
		return ret
	}
	buf := d.buf
	offs := d.offs
	nbitsTotal := d.nbitsTotal
	rem := d.rem
	for {
		nbitsTotal += EC_SYM_BITS
		rng <<= EC_SYM_BITS

		sym := uint32(rem)
		if int(offs) < len(buf) {
			rem = int32(buf[offs])
			offs++
		} else {
			rem = 0
		}
		sym = (sym<<EC_SYM_BITS | uint32(rem)) >> (EC_SYM_BITS - EC_CODE_EXTRA)
		val = ((val << EC_SYM_BITS) + (EC_SYM_MAX &^ sym)) & (EC_CODE_TOP - 1)
		if rng > EC_CODE_BOT {
			break
		}
	}
	d.offs = offs
	d.nbitsTotal = nbitsTotal
	d.rng = rng
	d.val = val
	d.rem = rem
	return ret
}

func (d *Decoder) decode(ft uint32) uint32 {
	d.ext = d.rng / ft
	s := d.val / d.ext
	if s+1 > ft {
		s = ft - 1
	}
	return ft - (s + 1)
}

// Decode returns the cumulative frequency fs (in [0, ft)) of the current symbol
// without advancing the decoder, the bit-exact port of libopus ec_decode. The
// caller maps fs to a symbol whose model interval [fl, fh) satisfies
// fl <= fs < fh, then must call [Decoder.Update] with that interval to consume
// the symbol. Decode caches the scale factor used by the paired Update.
func (d *Decoder) Decode(ft uint32) uint32 {
	return d.decode(ft)
}

// DecodeBin returns the cumulative frequency of the current symbol when the
// total frequency is the power of two 1<<bits, the bit-exact port of libopus
// ec_decode_bin. Like [Decoder.Decode] it does not advance the decoder; pair it
// with [Decoder.Update] using ft = 1<<bits. Using a power-of-two total lets the
// scale factor be a shift instead of a division.
//
//go:nosplit
func (d *Decoder) DecodeBin(bits uint) uint32 {
	if bits == 0 {
		return 0
	}
	ft := uint32(1) << bits
	d.ext = d.rng >> bits
	s := d.val / d.ext
	if s+1 > ft {
		s = ft - 1
	}
	return ft - (s + 1)
}

//go:nosplit
func (d *Decoder) update(fl, fh, ft uint32) {
	s := d.ext * (ft - fh)
	rng := d.rng - s
	if fl > 0 {
		rng = d.ext * (fh - fl)
	}
	d.rng, d.val = d.normalized(rng, d.val-s)
}

// Update consumes the symbol whose model interval is [fl, fh) out of total ft,
// advancing the decoder and renormalizing. It must be called after [Decoder.Decode]
// (or DecodeBin) with the interval selected from the returned cumulative
// frequency, and it reuses the scale factor cached by that call. This is the
// bit-exact port of libopus ec_dec_update and the inverse of [Encoder.Encode].
//
//go:nosplit
func (d *Decoder) Update(fl, fh, ft uint32) {
	d.update(fl, fh, ft)
}

// DecodeSymbol decodes a symbol given cumulative frequencies and updates state.
// fl: cumulative frequency of symbols before this one
// fh: cumulative frequency up to and including this symbol
// ft: total frequency (sum of all symbol frequencies)
//
// This implements the range decoder update: rng = s * fh, val = val - s * fl
// where s = rng / ft (the scale factor).
//
// Reference: libopus celt/entdec.c ec_dec_update()
func (d *Decoder) DecodeSymbol(fl, fh, ft uint32) {
	if ft == 0 {
		return
	}
	d.ext = d.rng / ft
	d.update(fl, fh, ft)
}

// DecodeRawBits reads `bits` raw bits from the raw-bit stream at the end of the
// buffer and returns them, the bit-exact port of libopus ec_dec_bits and the
// inverse of [Encoder.EncodeRawBits]. These bits bypass the range coder; reads
// past the start of the available raw region yield zero. Used for fine energy
// bits, PVQ sign bits, and similar fields.
func (d *Decoder) DecodeRawBits(bits uint) uint32 {
	if bits == 0 {
		return 0
	}

	endWindow := d.endWindow
	nendBits := int(d.nendBits)

	if nendBits < int(bits) {
		// Refill the whole 64-bit window. Reading ahead is purely a
		// buffering choice: the window holds the same end-of-buffer bit
		// stream, so every (call, bits) sequence sees identical values, and
		// exhaustion still pads with zeros from the same bit position.
		endOffs := d.endOffs
		storage := d.storage
		buf := d.buf
		for nendBits <= 56 && endOffs < storage {
			endOffs++
			endWindow |= uint64(buf[storage-endOffs]) << uint(nendBits)
			nendBits += 8
		}
		if nendBits < int(bits) {
			nendBits = int(bits)
		}
		d.endOffs = endOffs
	}

	val := uint32(endWindow) & ((1 << bits) - 1)
	d.endWindow = endWindow >> bits
	d.nendBits = int32(nendBits - int(bits))
	d.nbitsTotal += int32(bits)

	return val
}

// DecodeRawBit reads a single raw bit from the end of the buffer.
func (d *Decoder) DecodeRawBit() uint32 {
	if d.nendBits == 0 {
		if d.endOffs < d.storage {
			d.endOffs++
			d.endWindow |= uint64(d.buf[d.storage-d.endOffs])
			d.nendBits = 8
		} else {
			d.nendBits = 1
		}
	}
	val := uint32(d.endWindow) & 1
	d.endWindow >>= 1
	d.nendBits--
	d.nbitsTotal++
	return val
}
