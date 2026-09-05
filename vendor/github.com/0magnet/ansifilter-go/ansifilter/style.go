package ansifilter

// ElementStyle stores the text formatting state built up from SGR sequences.
// It is a direct port of the C++ ElementStyle class.
type ElementStyle struct {
	fgColor, bgColor               StyleColor
	bold, italic, underline, blink bool
	reset                          bool
	isNegativeMode, conceal        bool
	bgColorSet, fgColorSet         bool
	fgColID, bgColID               int
}

// newElementStyle returns a style in the default (reset) state.
func newElementStyle() ElementStyle {
	return ElementStyle{reset: true, fgColID: 0, bgColID: -1}
}

// IsItalic reports whether italic is set.
func (e *ElementStyle) IsItalic() bool { return e.italic }

// IsBlink reports whether blink is set.
func (e *ElementStyle) IsBlink() bool { return e.blink }

// IsBold reports whether bold is set.
func (e *ElementStyle) IsBold() bool { return e.bold }

// IsUnderline reports whether underline is set.
func (e *ElementStyle) IsUnderline() bool { return e.underline }

// IsConceal reports whether conceal is set.
func (e *ElementStyle) IsConceal() bool { return e.conceal }

// IsBgColorSet reports whether a background color was assigned.
func (e *ElementStyle) IsBgColorSet() bool { return e.bgColorSet }

// IsFgColorSet reports whether a foreground color was assigned.
func (e *ElementStyle) IsFgColorSet() bool { return e.fgColorSet }

// IsReset reports whether the style is in its default state.
func (e *ElementStyle) IsReset() bool { return e.reset }

// FgColor returns the foreground color.
func (e *ElementStyle) FgColor() StyleColor { return e.fgColor }

// BgColor returns the background color.
func (e *ElementStyle) BgColor() StyleColor { return e.bgColor }

func (e *ElementStyle) setBold(b bool)      { e.bold = b }
func (e *ElementStyle) setItalic(b bool)    { e.italic = b }
func (e *ElementStyle) setUnderline(b bool) { e.underline = b }
func (e *ElementStyle) setBlink(b bool)     { e.blink = b }
func (e *ElementStyle) setConceal(b bool)   { e.conceal = b }

func (e *ElementStyle) setFgColorStr(rgb string) {
	e.fgColor.SetRGB(rgb)
	e.fgColorSet = true
}

func (e *ElementStyle) setBgColorStr(rgb string) {
	e.bgColor.SetRGB(rgb)
	e.bgColorSet = true
}

func (e *ElementStyle) setFgColor(c StyleColor) {
	e.fgColor = c
	e.fgColorSet = true
}

func (e *ElementStyle) setBgColor(c StyleColor) {
	e.bgColor = c
	e.bgColorSet = true
}

func (e *ElementStyle) setFgColorID(id int) { e.fgColID = id }
func (e *ElementStyle) setBgColorID(id int) { e.bgColID = id }

// FgColorID returns the RTF color table index of the foreground color.
func (e *ElementStyle) FgColorID() int { return e.fgColID }

// BgColorID returns the RTF color table index of the background color.
func (e *ElementStyle) BgColorID() int { return e.bgColID }

// imageMode swaps foreground and background colors for reverse video (SGR 7
// and 27). The swap only happens on a genuine change of mode, and assigning
// the swapped colors marks both as explicitly set — which is why a reverse
// sequence on an otherwise default style emits two black colors.
func (e *ElementStyle) imageMode(negative bool) {
	if negative != e.isNegativeMode {
		swap := e.FgColor()
		e.setFgColor(e.BgColor())
		e.setBgColor(swap)
		e.isNegativeMode = !e.isNegativeMode
	}
}

// setReset marks the style as default and clears every attribute.
func (e *ElementStyle) setReset(b bool) {
	e.reset = b
	if e.reset {
		e.setFgColorStr("#000000")
		e.setFgColorID(0)
		e.setBgColorID(-1)
		e.bold = false
		e.italic = false
		e.underline = false
		e.conceal = false
		e.blink = false
		e.bgColorSet = false
		e.fgColorSet = false
	}
}

// styleInfo is a comparable snapshot of a style, used to build the derived
// stylesheet emitted by --derived-styles.
type styleInfo struct {
	fgColor, bgColor string
	isBold           bool
	isItalic         bool
	isConcealed      bool
	isBlink          bool
	isUnderLine      bool
}
