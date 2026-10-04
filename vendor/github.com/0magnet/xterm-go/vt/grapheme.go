package vt

//go:generate go run ./internal/ucdgen -o grapheme_tables.go

// Grapheme cluster segmentation for mode 2027 (DECSET ?2027), after the
// terminal-unicode-core spec: https://github.com/contour-terminal/terminal-unicode-core
//
// With the mode set, every code point that UAX #29 does not allow a break
// before joins the cell of the cluster it extends, and a cluster is as wide
// as its widest code point, with VS16 widening an emoji to two cells.
// GraphemeCharProperties has the same contract as CharProperties, so Print
// needs nothing but the choice of provider; this is the role xterm.js's
// addon-unicode-graphemes plays as a unicode provider.

// Packed per-code-point properties; see internal/ucdgen.
const (
	gcbOther = iota
	gcbCR
	gcbLF
	gcbControl
	gcbExtend
	gcbZWJ
	gcbRI
	gcbPrepend
	gcbSpacingMark
	gcbL
	gcbV
	gcbT
	gcbLV
	gcbLVT
)

const (
	propGCBMask   = 0xF
	propExtPict   = 1 << 4
	propInCBShift = 5
	propEmoji     = 1 << 7
	propWidthShft = 8

	incbConsonant = 1
	incbExtend    = 2
	incbLinker    = 3
)

// The cluster state carried in the char-kind field of a property value.
const (
	stGCBMask     = 0xF
	stEmojiShift  = 4 // 0, 1 = ExtPict Extend*, 2 = ExtPict Extend* ZWJ
	stRIOdd       = 1 << 6
	stInCBShift   = 7 // 0, 1 = Consonant [Extend|Linker]*, 2 = ... with a Linker
	stEmojiBase   = 1 << 9
	stGraphemeSet = 1 << 10 // marks state produced by this provider
)

// asciiProps caches the table for ASCII, by far the commonest input.
var asciiProps [128]uint16

func init() {
	for c := range asciiProps {
		asciiProps[c] = lookupGraphemeProps(uint32(c)) // #nosec G115 -- c < 128
	}
}

func lookupGraphemeProps(cp uint32) uint16 {
	lo, hi := 0, len(graphemeStarts)-1
	for lo < hi {
		mid := (lo + hi + 1) >> 1
		if graphemeStarts[mid] <= cp {
			lo = mid
		} else {
			hi = mid - 1
		}
	}
	return graphemeProps[lo]
}

func propsOf(cp uint32) uint16 {
	if cp < 128 {
		return asciiProps[cp]
	}
	return lookupGraphemeProps(cp)
}

// GraphemeWidth is the cell width of a code point standing alone under
// mode 2027.
func GraphemeWidth(cp uint32) int {
	return int(propsOf(cp)>>propWidthShft) & 3
}

// graphemeBreak applies the UAX #29 rules GB3 to GB13 between the cluster
// state st and a code point with the packed properties p.
func graphemeBreak(st uint32, p uint16) bool {
	prev := st & stGCBMask
	cur := uint32(p & propGCBMask)
	switch {
	case prev == gcbCR && cur == gcbLF: // GB3
		return false
	case prev == gcbCR || prev == gcbLF || prev == gcbControl: // GB4
		return true
	case cur == gcbCR || cur == gcbLF || cur == gcbControl: // GB5
		return true
	case prev == gcbL && (cur == gcbL || cur == gcbV || cur == gcbLV || cur == gcbLVT): // GB6
		return false
	case (prev == gcbLV || prev == gcbV) && (cur == gcbV || cur == gcbT): // GB7
		return false
	case (prev == gcbLVT || prev == gcbT) && cur == gcbT: // GB8
		return false
	case cur == gcbExtend || cur == gcbZWJ: // GB9
		return false
	case cur == gcbSpacingMark: // GB9a
		return false
	case prev == gcbPrepend: // GB9b
		return false
	case (st>>stInCBShift)&3 == 2 && (p>>propInCBShift)&3 == incbConsonant: // GB9c
		return false
	case prev == gcbZWJ && (st>>stEmojiShift)&3 == 2 && p&propExtPict != 0: // GB11
		return false
	case prev == gcbRI && cur == gcbRI && st&stRIOdd != 0: // GB12, GB13
		return false
	}
	return true // GB999
}

// nextGraphemeState advances the cluster state over a code point. joined
// says whether it extends the cluster st describes or begins a new one.
func nextGraphemeState(st uint32, p uint16, joined bool) uint32 {
	cur := uint32(p & propGCBMask)
	next := uint32(stGraphemeSet) | cur

	emoji := (st >> stEmojiShift) & 3
	switch {
	case p&propExtPict != 0:
		emoji = 1
	case cur == gcbExtend && emoji == 1:
	case cur == gcbZWJ && emoji == 1:
		emoji = 2
	default:
		emoji = 0
	}
	next |= emoji << stEmojiShift

	if cur == gcbRI && (!prevIsRI(st) || st&stRIOdd == 0) {
		next |= stRIOdd
	}

	incb := (st >> stInCBShift) & 3
	switch (p >> propInCBShift) & 3 {
	case incbConsonant:
		incb = 1
	case incbLinker:
		if incb != 0 {
			incb = 2
		}
	case incbExtend:
	default:
		incb = 0
	}
	next |= incb << stInCBShift

	if joined {
		next |= st & stEmojiBase
	} else if p&propEmoji != 0 {
		next |= stEmojiBase
	}
	return next
}

func prevIsRI(st uint32) bool { return st&stGCBMask == gcbRI }

// GraphemeCharProperties is CharProperties for mode 2027: it packs the
// width, the join flag and the cluster state of a code point given the
// packed properties of the one before it.
func GraphemeCharProperties(codepoint, preceding uint32) uint32 {
	p := propsOf(codepoint)
	width := int(p>>propWidthShft) & 3
	st := uint32(ExtractCharKind(preceding)) // #nosec G115 -- a 24-bit field
	oldWidth := ExtractWidth(preceding)

	// A state from the other provider, or none, starts a new cluster; so
	// does a zero-width predecessor, which has no cell to join.
	shouldJoin := preceding != 0 && st&stGraphemeSet != 0 && oldWidth != 0 &&
		!graphemeBreak(st, p)
	if shouldJoin {
		width = max(width, oldWidth)
		if codepoint == 0xFE0F && st&stEmojiBase != 0 { // VS16: emoji presentation
			width = 2
		}
	}
	next := nextGraphemeState(st, p, shouldJoin)
	return CreatePropertyValue(int(next), width, shouldJoin) // #nosec G115 -- 11 bits
}
