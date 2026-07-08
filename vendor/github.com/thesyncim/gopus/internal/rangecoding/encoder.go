package rangecoding

// Encoder implements the Opus range encoder (RFC 6716 Section 4.1), a bit-exact
// port of the libopus ec_enc context and the functions in celt/entenc.c. It is
// the exact symmetric inverse of [Decoder].
//
// Lifecycle: call [Encoder.Init] with a destination buffer, emit symbols with
// the Encode/EncodeBin/EncodeBit/EncodeICDF* methods and raw bits with
// [Encoder.EncodeRawBits], then call [Encoder.Done] to flush and obtain the
// packet. Range-coded symbols are written forward from the start of the buffer
// while raw bits are written backward from the end; [Encoder.Done] merges them.
//
// The field widths match libopus ec_ctx exactly because the integer
// wrap/truncation behavior is part of the bit-exact contract (see the package
// doc and the state-type parity test). In particular rem uses -1 as the
// "no buffered byte yet" sentinel, so it is signed.
type Encoder struct {
	buf        []byte // Output buffer (pre-allocated)
	storage    uint32 // Buffer capacity
	offs       uint32 // Current write offset
	endOffs    uint32 // End offset for raw bits (writes from end)
	endWindow  uint32 // Window for raw bits at end
	nendBits   int32  // Bits in end window; libopus ec_ctx.nend_bits is C int.
	nbitsTotal int32  // Total bits written; libopus ec_ctx.nbits_total is C int.
	rng        uint32 // Range size
	val        uint32 // Low end of range
	rem        int32  // Buffered byte for carry propagation; libopus ec_ctx.rem is C int.
	ext        uint32 // Count of pending 0xFF bytes
	err        int32  // Error flag; libopus ec_ctx.error is C int.
	shrunk     bool   // True if Shrink() was called (for CBR mode)
}

// Init initializes the encoder with the given output buffer.
// The buffer must be pre-allocated to the maximum expected output size.
func (e *Encoder) Init(buf []byte) {
	e.buf = buf
	e.storage = uint32(len(buf))
	e.offs = 0
	e.endOffs = 0
	e.endWindow = 0
	e.nendBits = 0
	e.nbitsTotal = EC_CODE_BITS + 1
	e.rng = EC_CODE_TOP
	e.val = 0
	e.rem = -1 // Sentinel: no bytes buffered yet
	e.ext = 0
	e.err = 0
	e.shrunk = false
}

// Shrink reduces the storage size to the given number of bytes.
// This is used in CBR mode to ensure the output packet is exactly the target size.
// The raw bits written from the end are moved to the new end position.
// This is a direct port of libopus celt/entenc.c ec_enc_shrink.
//
// After calling Shrink, Done() will return exactly 'size' bytes (padded with zeros).
func (e *Encoder) Shrink(size uint32) {
	if e.offs+e.endOffs > size {
		// Can't shrink to less than already written
		e.err = -1
		return
	}
	if size > e.storage {
		// Can't grow the buffer
		return
	}
	if e.endOffs > 0 {
		// Move end bytes to new position
		copy(e.buf[size-e.endOffs:size], e.buf[e.storage-e.endOffs:e.storage])
	}
	e.storage = size
	e.shrunk = true
}

// Limit reduces the storage size to the given number of bytes without forcing
// CBR-style output length. This is useful for VBR/CVBR paths that need a bit
// budget cap but still want Done() to return the actual number of bytes used.
// It mirrors ec_enc_shrink() without setting the "shrunk" flag.
func (e *Encoder) Limit(size uint32) {
	if e.offs+e.endOffs > size {
		// Can't shrink to less than already written
		e.err = -1
		return
	}
	if size > e.storage {
		// Can't grow the buffer
		return
	}
	if e.endOffs > 0 {
		// Move end bytes to new position
		copy(e.buf[size-e.endOffs:size], e.buf[e.storage-e.endOffs:e.storage])
	}
	e.storage = size
}

// carryOut handles carry propagation when outputting bytes.
// This is a direct port of libopus celt/entenc.c ec_enc_carry_out.
// NO byte complementing - the decoder handles that with its (255 &^ sym) formula.
//
// When symbol equals EC_SYM_MAX (0xFF), we increment ext (pending 0xFF bytes)
// because we can't know yet if there will be a carry from future symbols.
// When we get a non-0xFF symbol:
//   - Extract carry from high byte (symbol overflow)
//   - Write the buffered rem byte plus carry
//   - Write pending 0xFF bytes as (0xFF + carry) & 0xFF = 0x00 if carry, 0xFF if not
//   - Buffer the new symbol
//
// Reference: libopus celt/entenc.c ec_enc_carry_out
func (e *Encoder) carryOut(c int) {
	if c != EC_SYM_MAX {
		// c is not 0xFF, so we can flush buffered bytes
		carry := c >> EC_SYM_BITS // Extract carry from potential overflow

		// Write the previously buffered byte plus carry (if any)
		if e.rem >= 0 {
			e.writeByte(byte(int(e.rem) + carry))
		}

		// Write any pending 0xFF bytes, adjusted for carry
		if e.ext > 0 {
			sym := (EC_SYM_MAX + carry) & EC_SYM_MAX
			for e.ext > 0 {
				e.writeByte(byte(sym))
				e.ext--
			}
		}

		// Buffer the new symbol (low 8 bits only)
		e.rem = int32(c & EC_SYM_MAX)
	} else {
		// Symbol is 0xFF - can't flush yet because might need to carry
		e.ext++
	}
}

// normalize handles range renormalization and byte output.
// This follows libopus celt/entenc.c ec_enc_normalize exactly.
//
// The body is split so the common no-renormalization case (rng already above
// EC_CODE_BOT) stays small enough to inline into the encode entry points; the
// rare renormalization loop lives in normalizeSlow. Behavior is identical to a
// single while loop because the loop predicate is checked here first.
func (e *Encoder) normalize() {
	if e.rng <= EC_CODE_BOT {
		e.normalizeSlow()
	}
}

// normalizeSlow runs the renormalization loop once the range has dropped to or
// below EC_CODE_BOT. The encoder outputs the high bits of val, and the decoder
// reconstructs by reading those bytes and applying the inverse operation
// (255 &^ sym).
func (e *Encoder) normalizeSlow() {
	for {
		// Extract high bits to output via carry propagation
		e.carryOut(int(e.val >> EC_CODE_SHIFT))
		// Shift out 8 bits
		e.val = (e.val << EC_SYM_BITS) & (EC_CODE_TOP - 1)
		e.rng <<= EC_SYM_BITS
		e.nbitsTotal += int32(EC_SYM_BITS)
		if e.rng > EC_CODE_BOT {
			return
		}
	}
}

// writeByte writes a byte to the output buffer.
func (e *Encoder) writeByte(b byte) {
	if e.offs+e.endOffs >= e.storage {
		e.err = -1
		return
	}
	e.buf[e.offs] = b
	e.offs++
}

// Encode encodes a symbol that occupies the cumulative-frequency interval
// [fl, fh) out of a total frequency ft, the core range-coder primitive of
// RFC 6716 Section 4.1.2. It is the bit-exact port of libopus ec_encode and the
// inverse of [Decoder.Decode] followed by [Decoder.Update].
//
// fl is the sum of the frequencies of all symbols preceding this one, fh is fl
// plus this symbol's frequency, and ft is the sum of all symbol frequencies.
// Callers must ensure 0 <= fl < fh <= ft. The range is subdivided by the scale
// factor r = rng/ft, after which the encoder renormalizes.
func (e *Encoder) Encode(fl, fh, ft uint32) {
	r := e.rng / ft
	if fl > 0 {
		e.val += e.rng - r*(ft-fl)
		e.rng = r * (fh - fl)
	} else {
		e.rng -= r * (ft - fh)
	}
	e.normalize()
}

// EncodeBin encodes a symbol whose model interval is [fl, fh) when the total
// frequency is the power of two 1<<bits, the bit-exact port of libopus
// ec_encode_bin and the inverse of [Decoder.DecodeBin]+[Decoder.Update]. Using a
// power-of-two total lets the scale factor be a shift instead of a division.
func (e *Encoder) EncodeBin(fl, fh uint32, bits uint) {
	if bits == 0 {
		return
	}
	r := e.rng >> bits
	if fl > 0 {
		e.val += e.rng - r*((uint32(1)<<bits)-fl)
		e.rng = r * (fh - fl)
	} else {
		e.rng -= r * ((uint32(1) << bits) - fh)
	}
	e.normalize()
}

// EncodeICDF8 encodes a symbol using a uint8 ICDF table.
// s is the symbol to encode (0 to len(icdf)-1).
// icdf is the inverse cumulative distribution function table (decreasing values).
// ftb is the number of bits of precision (total = 1 << ftb).
//
// This is the uint8 variant of EncodeICDF for SILK tables.
func (e *Encoder) EncodeICDF8(s int, icdf []uint8, ftb uint) {
	e.EncodeICDF(s, icdf, ftb)
}

// EncodeICDF encodes a symbol using an inverse CDF table.
// s is the symbol to encode (0 to len(icdf)-1).
// icdf is the inverse cumulative distribution function table (decreasing values).
// ftb is the number of bits of precision (total = 1 << ftb).
//
// This is a direct port of libopus ec_enc_icdf.
func (e *Encoder) EncodeICDF(s int, icdf []uint8, ftb uint) {
	r := e.rng >> ftb
	if s > 0 {
		e.val += e.rng - r*uint32(icdf[s-1])
		e.rng = r * uint32(icdf[s-1]-icdf[s])
	} else {
		e.rng -= r * uint32(icdf[s])
	}
	e.normalize()
}

// EncodeICDF16 encodes a symbol using a uint16 ICDF table.
// Required because SILK tables use uint16 (256 doesn't fit in uint8).
// Per RFC 6716 Section 4.1.
//
// This is the uint16 variant of EncodeICDF, matching libopus ec_enc_icdf.
func (e *Encoder) EncodeICDF16(s int, icdf []uint16, ftb uint) {
	if s < 0 {
		s = 0
	}
	maxSymbol := len(icdf) - 1
	if s > maxSymbol {
		s = maxSymbol
	}

	r := e.rng >> ftb
	if s > 0 {
		e.val += e.rng - r*uint32(icdf[s-1])
		e.rng = r * uint32(icdf[s-1]-icdf[s])
	} else {
		e.rng -= r * uint32(icdf[s])
	}

	e.normalize()
}

// EncodeBit encodes a single bit with the given log probability.
// val is the bit to encode (0 or 1).
// logp is the log probability: P(0) = 1 - 1/(2^logp), P(1) = 1/(2^logp).
//
// Per RFC 6716 Section 4.1, the probability regions are:
// - [0, rng-r): bit = 0, probability = (2^logp - 1) / 2^logp
// - [rng-r, rng): bit = 1, probability = 1 / 2^logp
//
// For silence flag (logp=15): P(silence=1) = 1/32768, which is very rare.
//
// Encoder interval assignment (symmetric with decoder):
// - bit=0: use interval [0, threshold), rng = threshold, val unchanged
// - bit=1: use interval [threshold, rng), val += threshold, rng = r
func (e *Encoder) EncodeBit(val int, logp uint) {
	if logp == 0 {
		return
	}
	// logp is the log probability: P(1) = 1 / 2^logp (rare, bottom region).
	r := e.rng
	s := r >> logp
	if val != 0 {
		e.val += r - s
		e.rng = s
	} else {
		e.rng = r - s
	}
	e.normalize()
}

// Done finalizes the stream and returns the encoded packet. It emits the
// minimum number of bits needed to unambiguously identify the final range
// interval, flushes any buffered carry byte and pending raw bits, and merges
// the forward range-coded bytes with the backward raw-bit bytes into one packet.
//
// This is the bit-exact port of libopus ec_enc_done. The returned slice aliases
// the buffer passed to [Encoder.Init]; copy it if it must outlive the next use
// of the buffer. After Done the encoder must be re-initialized before reuse.
// Check [Encoder.Error] for buffer overflow during encoding.
func (e *Encoder) Done() []byte {
	// Compute how many bits we need to output to uniquely identify the interval.
	l := EC_CODE_BITS - int(ilog(e.rng))

	// Compute mask for rounding
	var msk uint32 = (EC_CODE_TOP - 1) >> uint(l)

	// Round up to alignment boundary
	end := (e.val + msk) & ^msk

	// Check if end is still within [val, val+rng)
	if (end | msk) >= e.val+e.rng {
		l++
		msk >>= 1
		end = (e.val + msk) & ^msk
	}

	// Output remaining bytes via carry propagation
	for l > 0 {
		e.carryOut(int(end >> EC_CODE_SHIFT))
		end = (end << EC_SYM_BITS) & (EC_CODE_TOP - 1)
		l -= EC_SYM_BITS
	}

	// Final symbol buffer flush
	if e.rem >= 0 || e.ext > 0 {
		e.carryOut(0)
	}

	// Flush any buffered raw bits as end bytes.
	window := e.endWindow
	used := int(e.nendBits)
	for used >= EC_SYM_BITS {
		e.writeEndByte(byte(window & EC_SYM_MAX))
		window >>= EC_SYM_BITS
		used -= EC_SYM_BITS
	}

	if e.err == 0 {
		if e.buf != nil {
			start := int(e.offs)
			endIdx := int(e.storage - e.endOffs)
			for i := start; i < endIdx; i++ {
				e.buf[i] = 0
			}
		}
		if used > 0 {
			if e.endOffs >= e.storage {
				e.err = -1
			} else {
				usable := max(-l, 0)
				if int(e.offs+e.endOffs) >= int(e.storage) && usable < used {
					if usable >= 32 {
						// No masking needed for full 32-bit window.
					} else if usable <= 0 {
						window = 0
					} else {
						window &= (uint32(1) << usable) - 1
					}
					e.err = -1
				}
				idx := int(e.storage - e.endOffs - 1)
				if idx >= 0 && idx < len(e.buf) {
					e.buf[idx] |= byte(window)
				}
			}
		}
	}

	if e.err != 0 {
		if int(e.storage) <= len(e.buf) {
			return e.buf[:e.storage]
		}
		return e.buf
	}

	// Calculate packed size
	padSize := 0
	if !e.shrunk && used > 0 && e.offs+e.endOffs < e.storage {
		padSize = 1
		if int(e.offs) < len(e.buf) {
			e.buf[e.offs] = byte(window)
		}
	}
	packedSize := int(e.offs + e.endOffs + uint32(padSize))

	// For CBR mode (when Shrink was called), return exactly storage bytes.
	if e.shrunk {
		packedSize = int(e.storage)
	}

	// In CBR/shrunk mode, keep the exact libopus layout:
	// range-coded bytes at the front, raw/end bytes at the very end, zero gap in between.
	// Non-shrunk mode returns a packed slice for convenience.
	if e.endOffs > 0 && !e.shrunk {
		dst := int(e.offs) + padSize
		copy(e.buf[dst:], e.buf[e.storage-e.endOffs:e.storage])
	}

	if packedSize < 0 {
		packedSize = 0
	}
	if packedSize > int(e.storage) {
		e.err = -1
		packedSize = int(e.storage)
	}
	if packedSize > len(e.buf) {
		packedSize = len(e.buf)
	}

	return e.buf[:packedSize]
}

// Tell returns the number of bits written to the combined stream so far,
// rounded up to the nearest whole bit. This is the libopus ec_tell macro
// (nbits_total - EC_ILOG(rng)) and counts both range-coded symbols and raw bits.
func (e *Encoder) Tell() int {
	return int(e.nbitsTotal) - int(ilog(e.rng))
}

// TellFrac returns the number of bits written to the combined stream so far in
// 1/8th-bit (BITRES) precision; divide by 8 to compare with [Encoder.Tell].
// It is the bit-exact port of libopus ec_tell_frac, including the eight-entry
// correction table that refines the fractional bit count from the range.
func (e *Encoder) TellFrac() int {
	correction := [8]uint32{35733, 38967, 42495, 46340, 50535, 55109, 60097, 65535}

	nbits := int(e.nbitsTotal) << 3
	l := int(ilog(e.rng))
	r := e.rng >> (uint(l) - 16)
	b := int((r >> 12) - 8)
	if r > correction[b] {
		b++
	}
	return nbits - ((l << 3) + b)
}

// Range returns the current range value.
func (e *Encoder) Range() uint32 {
	return e.rng
}

// Offs returns the current write offset in bytes.
func (e *Encoder) Offs() uint32 {
	return e.offs
}

// Buffer returns the underlying output buffer.
// Callers must treat the returned slice as read-only.
func (e *Encoder) Buffer() []byte {
	return e.buf
}

// Val returns the current val, the low end of the range.
func (e *Encoder) Val() uint32 {
	return e.val
}

// Rem returns the stored remainder byte.
func (e *Encoder) Rem() int {
	return int(e.rem)
}

// StorageBits returns the total number of bits in the output buffer.
func (e *Encoder) StorageBits() int {
	return int(e.storage * 8)
}

// Storage returns the storage capacity in bytes.
func (e *Encoder) Storage() int {
	return int(e.storage)
}

// Ext returns the extension count.
func (e *Encoder) Ext() uint32 {
	return e.ext
}

// Error returns the encoder error flag (libopus ec_ctx.error). A non-zero value
// means the output did not fit in the buffer passed to [Encoder.Init] (the
// forward range-coded and backward raw-bit streams collided), so the produced
// packet is truncated and not decodable.
func (e *Encoder) Error() int {
	return int(e.err)
}

// EncoderState captures the encoder state for save/restore operations.
// This is used by theta RDO to try different quantization choices.
type EncoderState struct {
	storage    uint32
	offs       uint32
	endOffs    uint32
	endWindow  uint32
	nendBits   int32
	nbitsTotal int32
	rng        uint32
	val        uint32
	rem        int32
	ext        uint32
	err        int32
	shrunk     bool
	buf        []byte
}

// SaveState captures the current encoder state for later restoration.
// This allows trying different encoding choices and restoring to try again.
func (e *Encoder) SaveState() *EncoderState {
	state := &EncoderState{}
	e.SaveStateInto(state)
	return state
}

// SaveStateInto captures the current encoder state into a pre-allocated state struct.
// This is the allocation-free version of SaveState for hot paths.
func (e *Encoder) SaveStateInto(state *EncoderState) {
	state.storage = e.storage
	state.offs = e.offs
	state.endOffs = e.endOffs
	state.endWindow = e.endWindow
	state.nendBits = e.nendBits
	state.nbitsTotal = e.nbitsTotal
	state.rng = e.rng
	state.val = e.val
	state.rem = e.rem
	state.ext = e.ext
	state.err = e.err
	state.shrunk = e.shrunk

	// Save the whole active storage. libopus theta RDO restores a shallow
	// ec_ctx plus the byte span dirtied by the first trial; saving the full
	// active buffer preserves the same middle-gap bytes for every caller.
	if e.storage > 0 {
		if cap(state.buf) < int(e.storage) {
			state.buf = make([]byte, e.storage)
		} else {
			state.buf = state.buf[:e.storage]
		}
		copy(state.buf, e.buf[:e.storage])
	} else {
		state.buf = state.buf[:0]
	}
}

func (e *Encoder) restoreScalarState(state *EncoderState) {
	e.storage = state.storage
	e.offs = state.offs
	e.endOffs = state.endOffs
	e.endWindow = state.endWindow
	e.nendBits = state.nendBits
	e.nbitsTotal = state.nbitsTotal
	e.rng = state.rng
	e.val = state.val
	e.rem = state.rem
	e.ext = state.ext
	e.err = state.err
	e.shrunk = state.shrunk
}

// RestoreState restores the encoder to a previously saved state.
func (e *Encoder) RestoreState(state *EncoderState) {
	e.restoreScalarState(state)
	if len(state.buf) > 0 {
		copy(e.buf[:state.storage], state.buf)
	}
}

// RestoreStateShallow mirrors a libopus ec_ctx struct assignment such as
// celt/bands.c:*ec = ec_save: scalar fields are restored, while the shared
// packet buffer keeps bytes dirtied by speculative encoding.
func (e *Encoder) RestoreStateShallow(state *EncoderState) {
	e.restoreScalarState(state)
}

// RangeBytes returns the number of range-coded bytes written.
// This mirrors libopus ec_range_bytes.
func (e *Encoder) RangeBytes() int {
	return int(e.offs)
}

// State returns the internal range encoder state (rng, val).
// Useful for bit-exact comparisons against libopus in tests.
func (e *Encoder) State() (uint32, uint32) {
	return e.rng, e.val
}

// PatchInitialBits overwrites the first few bits in the range coder stream.
// This mirrors libopus ec_enc_patch_initial_bits and is used for VAD/LBRR flag
// encoding in SILK packets where the flags must be written at the packet start
// but their values are only known after encoding the frame data.
func (e *Encoder) PatchInitialBits(val uint32, nbits uint) {
	if nbits <= 0 || nbits > EC_SYM_BITS {
		e.err = -1
		return
	}
	shift := EC_SYM_BITS - nbits
	mask := (uint32(1)<<nbits - 1) << shift
	if e.offs > 0 {
		e.buf[0] = (e.buf[0] &^ byte(mask)) | byte(val<<shift)
	} else if e.rem >= 0 {
		e.rem = int32((uint32(e.rem) &^ mask) | (val << shift))
	} else if e.rng <= (EC_CODE_TOP >> nbits) {
		shiftedMask := mask << EC_CODE_SHIFT
		e.val = (e.val &^ shiftedMask) | (val << (EC_CODE_SHIFT + shift))
	} else {
		// If we reach here, it means we're trying to patch bits before
		// the range coder has progressed enough to stabilize the first bits.
		e.err = -1
	}
}

// EncodeUniform encodes a uniformly distributed integer val in the range
// [0, ft). It is the bit-exact port of libopus ec_enc_uint and the inverse of
// [Decoder.DecodeUniform]. For ft larger than 1<<EC_UINT_BITS the high
// EC_UINT_BITS bits are range-coded and the low bits are written as raw bits, so
// large uniform values consume part of each stream. Used for fine energy bits,
// PVQ indices, and similar uniformly distributed fields.
func (e *Encoder) EncodeUniform(val uint32, ft uint32) {
	if ft <= 1 {
		return // Only one possible value, nothing to encode
	}

	// Calculate number of bits needed
	ftb := uint(ilog(ft - 1))
	if ftb > EC_SYM_BITS {
		// Multi-byte case: encode high bits with range coder, low bits raw
		ftb -= EC_SYM_BITS
		ft1 := (ft - 1) >> ftb
		e.encodeUniformInternal(val>>ftb, ft1+1)
		// Encode low bits raw
		e.EncodeRawBits(val&((1<<ftb)-1), ftb)
	} else {
		// Single-byte case
		e.encodeUniformInternal(val, ft)
	}
}

// encodeUniformInternal encodes a uniform value when ft <= 256.
// Uses the same approach as Encode() for uniformly distributed values.
func (e *Encoder) encodeUniformInternal(val uint32, ft uint32) {
	// For uniform distribution, fl=val, fh=val+1
	// Using the Encode formula adapted for uniform case
	r := e.rng / ft
	if val > 0 {
		e.val += e.rng - r*(ft-val)
		e.rng = r
	} else {
		// val == 0: stay at current position
		e.rng -= r * (ft - 1)
	}
	e.normalize()
}

// EncodeRawBits writes the low `bits` bits of val directly to the raw-bit stream
// at the end of the buffer, bypassing the range coder. It is the bit-exact port
// of libopus ec_enc_bits and the inverse of [Decoder.DecodeRawBits]. Raw bits
// grow backward from the end of the packet while range-coded symbols grow
// forward from the start; [Encoder.Done] reconciles the two streams.
func (e *Encoder) EncodeRawBits(val uint32, bits uint) {
	if bits == 0 {
		return
	}
	window := e.endWindow
	used := int(e.nendBits)
	if used+int(bits) > EC_WINDOW_SIZE {
		for used >= EC_SYM_BITS {
			e.writeEndByte(byte(window & EC_SYM_MAX))
			window >>= EC_SYM_BITS
			used -= EC_SYM_BITS
		}
	}
	window |= val << used
	used += int(bits)
	e.endWindow = window
	e.nendBits = int32(used)
	e.nbitsTotal += int32(bits)
}

// writeEndByte writes a byte to the end of the buffer (growing backwards).
func (e *Encoder) writeEndByte(b byte) {
	if e.offs+e.endOffs >= e.storage {
		e.err = -1
		return
	}
	e.endOffs++
	e.buf[e.storage-e.endOffs] = b
}
