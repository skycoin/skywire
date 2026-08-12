package vt

// Port of src/common/data/Charsets.ts — the VT100 national replacement
// character sets (ISO 2022 designation). A nil Charset means US-ASCII
// pass-through.

// Charsets maps the designation byte (final char of ESC ( x etc.) to
// its translation table.
var Charsets = map[byte]Charset{
	// DEC Special Character and Line Drawing Set.
	// Reference: http://vt100.net/docs/vt102-ug/table5-13.html
	'0': {
		'`': '◆', // '◆'
		'a': '▒', // '▒'
		'b': '␉', // '␉' (HT)
		'c': '␌', // '␌' (FF)
		'd': '␍', // '␍' (CR)
		'e': '␊', // '␊' (LF)
		'f': '°', // '°'
		'g': '±', // '±'
		'h': '␤', // '␤' (NL)
		'i': '␋', // '␋' (VT)
		'j': '┘', // '┘'
		'k': '┐', // '┐'
		'l': '┌', // '┌'
		'm': '└', // '└'
		'n': '┼', // '┼'
		'o': '⎺', // '⎺'
		'p': '⎻', // '⎻'
		'q': '─', // '─'
		'r': '⎼', // '⎼'
		's': '⎽', // '⎽'
		't': '├', // '├'
		'u': '┤', // '┤'
		'v': '┴', // '┴'
		'w': '┬', // '┬'
		'x': '│', // '│'
		'y': '≤', // '≤'
		'z': '≥', // '≥'
		'{': 'π', // 'π'
		'|': '≠', // '≠'
		'}': '£', // '£'
		'~': '·', // '·'
	},
	// British character set (ESC (A)
	'A': {
		'#': '£',
	},
	// United States character set (ESC (B) — identity
	'B': nil,
	// Dutch character set (ESC (4)
	'4': {
		'#': '£',
		'@': '¾',
		// xterm.js maps this to the two-char string "ij" which its
		// print path truncates to 'i' via charCodeAt(0); we use the
		// single-codepoint ligature the VT220 table intends.
		'[':  'ĳ',
		'\\': '½',
		']':  '|',
		'{':  '¨',
		'|':  'f',
		'}':  '¼',
		'~':  '´',
	},
	// Finnish character set (ESC (C or ESC (5)
	'C': charsetFinnish,
	'5': charsetFinnish,
	// French character set (ESC (R)
	'R': {
		'#':  '£',
		'@':  'à',
		'[':  '°',
		'\\': 'ç',
		']':  '§',
		'{':  'é',
		'|':  'ù',
		'}':  'è',
		'~':  '¨',
	},
	// French Canadian character set (ESC (Q)
	'Q': {
		'@':  'à',
		'[':  'â',
		'\\': 'ç',
		']':  'ê',
		'^':  'î',
		'`':  'ô',
		'{':  'é',
		'|':  'ù',
		'}':  'è',
		'~':  'û',
	},
	// German character set (ESC (K)
	'K': {
		'@':  '§',
		'[':  'Ä',
		'\\': 'Ö',
		']':  'Ü',
		'{':  'ä',
		'|':  'ö',
		'}':  'ü',
		'~':  'ß',
	},
	// Italian character set (ESC (Y)
	'Y': {
		'#':  '£',
		'@':  '§',
		'[':  '°',
		'\\': 'ç',
		']':  'é',
		'`':  'ù',
		'{':  'à',
		'|':  'ò',
		'}':  'è',
		'~':  'ì',
	},
	// Norwegian/Danish character set (ESC (E or ESC (6)
	'E': charsetNorwegian,
	'6': charsetNorwegian,
	// Spanish character set (ESC (Z)
	'Z': {
		'#':  '£',
		'@':  '§',
		'[':  '¡',
		'\\': 'Ñ',
		']':  '¿',
		'{':  '°',
		'|':  'ñ',
		'}':  'ç',
	},
	// Swedish character set (ESC (H or ESC (7)
	'H': charsetSwedish,
	'7': charsetSwedish,
	// Swiss character set (ESC (=)
	'=': {
		'#':  'ù',
		'@':  'à',
		'[':  'é',
		'\\': 'ç',
		']':  'ê',
		'^':  'î',
		'_':  'è',
		'`':  'ô',
		'{':  'ä',
		'|':  'ö',
		'}':  'ü',
		'~':  'û',
	},
}

var charsetFinnish = Charset{
	'[':  'Ä',
	'\\': 'Ö',
	']':  'Å',
	'^':  'Ü',
	'`':  'é',
	'{':  'ä',
	'|':  'ö',
	'}':  'å',
	'~':  'ü',
}

var charsetNorwegian = Charset{
	'@':  'Ä',
	'[':  'Æ',
	'\\': 'Ø',
	']':  'Å',
	'^':  'Ü',
	'`':  'ä',
	'{':  'æ',
	'|':  'ø',
	'}':  'å',
	'~':  'ü',
}

var charsetSwedish = Charset{
	'@':  'É',
	'[':  'Ä',
	'\\': 'Ö',
	']':  'Å',
	'^':  'Ü',
	'`':  'é',
	'{':  'ä',
	'|':  'ö',
	'}':  'å',
	'~':  'ü',
}

// DefaultCharset is the default (US) character set.
var DefaultCharset Charset // nil = pass-through
