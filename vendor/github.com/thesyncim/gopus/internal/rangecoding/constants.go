package rangecoding

// Range coder constants from RFC 6716 Section 4.1 and libopus celt/mfrngcod.h /
// celt/entcode.h. The doc strings mirror the reference header comments; the
// numeric expressions are written exactly as in libopus so the derived values
// are provably identical.
const (
	// EC_SYM_BITS is the number of bits to output/input at a time (one symbol).
	EC_SYM_BITS = 8
	// EC_CODE_BITS is the total number of bits in each of the rng/val state registers.
	EC_CODE_BITS = 32
	// EC_SYM_MAX is the maximum symbol value (0xFF); equals (1<<EC_SYM_BITS)-1.
	EC_SYM_MAX = (1 << EC_SYM_BITS) - 1
	// EC_CODE_TOP is the carry bit of the high-order range symbol (0x80000000).
	EC_CODE_TOP = 1 << (EC_CODE_BITS - 1)
	// EC_CODE_BOT is the low-order bit of the high-order range symbol
	// (0x00800000); renormalization keeps rng strictly above this value.
	EC_CODE_BOT = EC_CODE_TOP >> EC_SYM_BITS
	// EC_CODE_SHIFT is the shift to move a symbol into the high-order position (23).
	EC_CODE_SHIFT = EC_CODE_BITS - EC_SYM_BITS - 1
	// EC_CODE_EXTRA is the number of bits available for the last, partial symbol
	// in the code field (7).
	EC_CODE_EXTRA = (EC_CODE_BITS-2)%EC_SYM_BITS + 1
	// EC_UINT_BITS is the number of bits used for the range-coded part of
	// unsigned integers (ec_enc_uint/ec_dec_uint); the remainder are raw bits.
	EC_UINT_BITS = 8
	// EC_WINDOW_SIZE is the number of bits in the raw-bit end window
	// (sizeof(ec_window)*CHAR_BIT in libopus, with ec_window = opus_uint32).
	EC_WINDOW_SIZE = 32
)
