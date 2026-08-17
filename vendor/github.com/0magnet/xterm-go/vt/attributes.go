package vt

// Buffer cell constants (port of buffer/Constants.ts).
const (
	DefaultColor = 0
	DefaultAttr  = 0<<18 | DefaultColor<<9 | 256<<0
	DefaultExt   = 0

	NullCellChar  = 0
	NullCellWidth = 1
	NullCellCode  = 0

	WhitespaceCellChar  = ' '
	WhitespaceCellWidth = 1
	WhitespaceCellCode  = 32
)

// Content bit masks: bit 1..21 codepoint, bit 22 combined flag,
// bit 23..24 wcwidth.
const (
	ContentCodepointMask  uint32 = 0x1FFFFF
	ContentIsCombinedMask uint32 = 0x200000
	ContentHasContentMask uint32 = 0x3FFFFF
	ContentWidthMask      uint32 = 0xC00000
	ContentWidthShift            = 22
)

// Attribute color bits.
const (
	AttrBlueMask   uint32 = 0xFF
	AttrBlueShift         = 0
	AttrPColorMask uint32 = 0xFF
	AttrGreenMask  uint32 = 0xFF00
	AttrGreenShift        = 8
	AttrRedMask    uint32 = 0xFF0000
	AttrRedShift          = 16

	// color mode: DEFAULT (0) | P16 (1) | P256 (2) | RGB (3)
	AttrCMMask    uint32 = 0x3000000
	AttrCMDefault uint32 = 0
	AttrCMP16     uint32 = 0x1000000
	AttrCMP256    uint32 = 0x2000000
	AttrCMRGB     uint32 = 0x3000000

	AttrRGBMask uint32 = 0xFFFFFF
)

// FG flag bits (bit 27..32).
const (
	FgInverse       uint32 = 0x4000000
	FgBold          uint32 = 0x8000000
	FgUnderline     uint32 = 0x10000000
	FgBlink         uint32 = 0x20000000
	FgInvisible     uint32 = 0x40000000
	FgStrikethrough uint32 = 0x80000000
)

// BG flag bits (bit 27..32, upper 2 unused).
const (
	BgItalic      uint32 = 0x4000000
	BgDim         uint32 = 0x8000000
	BgHasExtended uint32 = 0x10000000
	BgProtected   uint32 = 0x20000000
	BgOverline    uint32 = 0x40000000
)

// Extended attribute bits.
const (
	ExtUnderlineStyle uint32 = 0x1C000000
	ExtVariantOffset  uint32 = 0xE0000000
)

// Underline styles.
const (
	UnderlineNone   = 0
	UnderlineSingle = 1
	UnderlineDouble = 2
	UnderlineCurly  = 3
	UnderlineDotted = 4
	UnderlineDashed = 5
)

// ExtendedAttrs holds underline style/color and url id of a cell.
type ExtendedAttrs struct {
	ext   uint32
	URLID int
}

// Ext returns the packed extended attribute value.
func (e *ExtendedAttrs) Ext() uint32 {
	if e.URLID != 0 {
		return (e.ext & ^ExtUnderlineStyle) | (uint32(e.UnderlineStyle()) << 26) // #nosec G115 -- the value is masked to its bitfield width on the same line
	}
	return e.ext
}

// SetExt sets the packed extended attribute value.
func (e *ExtendedAttrs) SetExt(value uint32) { e.ext = value }

// UnderlineStyle returns the underline style.
func (e *ExtendedAttrs) UnderlineStyle() int {
	if e.URLID != 0 {
		return UnderlineDashed
	}
	return int((e.ext & ExtUnderlineStyle) >> 26)
}

// SetUnderlineStyle sets the underline style.
func (e *ExtendedAttrs) SetUnderlineStyle(value int) {
	e.ext &= ^ExtUnderlineStyle
	e.ext |= (uint32(value) << 26) & ExtUnderlineStyle // #nosec G115 -- the value is masked to its bitfield width on the same line
}

// UnderlineColor returns the packed underline color (mode+rgb).
func (e *ExtendedAttrs) UnderlineColor() uint32 {
	return e.ext & (AttrCMMask | AttrRGBMask)
}

// SetUnderlineColor sets the packed underline color.
func (e *ExtendedAttrs) SetUnderlineColor(value uint32) {
	e.ext &= ^(AttrCMMask | AttrRGBMask)
	e.ext |= value & (AttrCMMask | AttrRGBMask)
}

// HasUnderlineColor mirrors the JS `~underlineColor` truthiness check;
// since the getter masks to a non-negative value it can never be -1,
// making the check effectively always true (kept for faithfulness).
func (e *ExtendedAttrs) HasUnderlineColor() bool { return true }

// UnderlineVariantOffset returns the variant offset.
func (e *ExtendedAttrs) UnderlineVariantOffset() int {
	val := int32(e.ext&ExtVariantOffset) >> 29 // #nosec G115 -- the value is masked to its bitfield width on the same line
	if val < 0 {
		return int(uint32(val) ^ 0xFFFFFFF8)
	}
	return int(val)
}

// SetUnderlineVariantOffset sets the variant offset.
func (e *ExtendedAttrs) SetUnderlineVariantOffset(value int) {
	e.ext &= ^ExtVariantOffset
	e.ext |= (uint32(value) << 29) & ExtVariantOffset // #nosec G115 -- the value is masked to its bitfield width on the same line
}

// NewExtendedAttrs creates empty extended attributes.
func NewExtendedAttrs() *ExtendedAttrs {
	return &ExtendedAttrs{}
}

// Clone returns a copy.
func (e *ExtendedAttrs) Clone() *ExtendedAttrs {
	return &ExtendedAttrs{ext: e.ext, URLID: e.URLID}
}

// IsEmpty reports whether the attrs hold nothing persistent.
func (e *ExtendedAttrs) IsEmpty() bool {
	return e.UnderlineStyle() == UnderlineNone && e.URLID == 0
}

// AttributeData is the port of AttributeData: packed fg/bg attributes
// plus extended attributes.
type AttributeData struct {
	Fg       uint32
	Bg       uint32
	Extended *ExtendedAttrs
}

// NewAttributeData creates empty attributes.
func NewAttributeData() *AttributeData {
	return &AttributeData{Extended: NewExtendedAttrs()}
}

// Clone returns a deep copy.
func (a *AttributeData) Clone() *AttributeData {
	return &AttributeData{Fg: a.Fg, Bg: a.Bg, Extended: a.Extended.Clone()}
}

// ToColorRGB unpacks a color value to [r, g, b].
func ToColorRGB(value uint32) [3]int {
	return [3]int{
		int(value >> AttrRedShift & 255),
		int(value >> AttrGreenShift & 255),
		int(value & 255),
	}
}

// FromColorRGB packs [r, g, b] into a color value.
func FromColorRGB(value [3]int) uint32 {
	return uint32(value[0]&255)<<AttrRedShift | uint32(value[1]&255)<<AttrGreenShift | uint32(value[2]&255)
}

// Flag accessors.
func (a *AttributeData) IsInverse() bool { return a.Fg&FgInverse != 0 }
func (a *AttributeData) IsBold() bool    { return a.Fg&FgBold != 0 }
func (a *AttributeData) IsUnderline() bool {
	if a.HasExtendedAttrs() && a.Extended.UnderlineStyle() != UnderlineNone {
		return true
	}
	return a.Fg&FgUnderline != 0
}
func (a *AttributeData) IsBlink() bool         { return a.Fg&FgBlink != 0 }
func (a *AttributeData) IsInvisible() bool     { return a.Fg&FgInvisible != 0 }
func (a *AttributeData) IsItalic() bool        { return a.Bg&BgItalic != 0 }
func (a *AttributeData) IsDim() bool           { return a.Bg&BgDim != 0 }
func (a *AttributeData) IsStrikethrough() bool { return a.Fg&FgStrikethrough != 0 }
func (a *AttributeData) IsProtected() bool     { return a.Bg&BgProtected != 0 }
func (a *AttributeData) IsOverline() bool      { return a.Bg&BgOverline != 0 }

// Color mode accessors.
func (a *AttributeData) GetFgColorMode() uint32 { return a.Fg & AttrCMMask }
func (a *AttributeData) GetBgColorMode() uint32 { return a.Bg & AttrCMMask }
func (a *AttributeData) IsFgRGB() bool          { return a.Fg&AttrCMMask == AttrCMRGB }
func (a *AttributeData) IsBgRGB() bool          { return a.Bg&AttrCMMask == AttrCMRGB }
func (a *AttributeData) IsFgPalette() bool {
	cm := a.Fg & AttrCMMask
	return cm == AttrCMP16 || cm == AttrCMP256
}
func (a *AttributeData) IsBgPalette() bool {
	cm := a.Bg & AttrCMMask
	return cm == AttrCMP16 || cm == AttrCMP256
}
func (a *AttributeData) IsFgDefault() bool        { return a.Fg&AttrCMMask == 0 }
func (a *AttributeData) IsBgDefault() bool        { return a.Bg&AttrCMMask == 0 }
func (a *AttributeData) IsAttributeDefault() bool { return a.Fg == 0 && a.Bg == 0 }

// GetFgColor returns the fg color value (-1 for default).
func (a *AttributeData) GetFgColor() int {
	switch a.Fg & AttrCMMask {
	case AttrCMP16, AttrCMP256:
		return int(a.Fg & AttrPColorMask)
	case AttrCMRGB:
		return int(a.Fg & AttrRGBMask)
	default:
		return -1
	}
}

// GetBgColor returns the bg color value (-1 for default).
func (a *AttributeData) GetBgColor() int {
	switch a.Bg & AttrCMMask {
	case AttrCMP16, AttrCMP256:
		return int(a.Bg & AttrPColorMask)
	case AttrCMRGB:
		return int(a.Bg & AttrRGBMask)
	default:
		return -1
	}
}

// HasExtendedAttrs reports whether extended attrs are present.
func (a *AttributeData) HasExtendedAttrs() bool { return a.Bg&BgHasExtended != 0 }

// UpdateExtended syncs the HAS_EXTENDED flag with the extended content.
func (a *AttributeData) UpdateExtended() {
	if a.Extended.IsEmpty() {
		a.Bg &= ^BgHasExtended
	} else {
		a.Bg |= BgHasExtended
	}
}

// GetUnderlineColor returns the underline color (falls back to fg).
func (a *AttributeData) GetUnderlineColor() int {
	if a.Bg&BgHasExtended != 0 && a.Extended.HasUnderlineColor() {
		switch a.Extended.UnderlineColor() & AttrCMMask {
		case AttrCMP16, AttrCMP256:
			return int(a.Extended.UnderlineColor() & AttrPColorMask)
		case AttrCMRGB:
			return int(a.Extended.UnderlineColor() & AttrRGBMask)
		default:
			return a.GetFgColor()
		}
	}
	return a.GetFgColor()
}

// GetUnderlineStyle returns the effective underline style.
func (a *AttributeData) GetUnderlineStyle() int {
	if a.Fg&FgUnderline == 0 {
		return UnderlineNone
	}
	if a.Bg&BgHasExtended != 0 {
		return a.Extended.UnderlineStyle()
	}
	return UnderlineSingle
}
