package vt

// Port of src/common/input/Keyboard.ts — translate DOM keydown events
// into the escape sequences a pty expects.

// KeyboardResultType describes what should happen for a key event.
type KeyboardResultType int

// Keyboard result types.
const (
	KeySendKey KeyboardResultType = iota
	KeySelectAll
	KeyPageUp
	KeyPageDown
)

// KeyboardEvent is the subset of the DOM KeyboardEvent used for
// evaluation (IKeyboardEvent).
type KeyboardEvent struct {
	AltKey   bool
	CtrlKey  bool
	ShiftKey bool
	MetaKey  bool
	KeyCode  int
	Key      string
	Code     string
}

// KeyboardResult is the evaluation outcome (IKeyboardResult).
type KeyboardResult struct {
	Type KeyboardResultType
	// Cancel reports whether to cancel event propagation.
	Cancel bool
	// Key is the data to send to the pty ("" = nothing).
	Key string
}

// additional C0 codes used by the keyboard evaluation
const (
	c0NUL = "\x00"
	c0FS  = "\x1c"
	c0GS  = "\x1d"
	c0US  = "\x1f"
	c0DEL = "\x7f"
)

// keycodeKeyMappings holds regular + shifted key chars for digits and
// special chars.
var keycodeKeyMappings = map[int][2]string{
	// digits 0-9
	48: {"0", ")"},
	49: {"1", "!"},
	50: {"2", "@"},
	51: {"3", "#"},
	52: {"4", "$"},
	53: {"5", "%"},
	54: {"6", "^"},
	55: {"7", "&"},
	56: {"8", "*"},
	57: {"9", "("},

	// special chars
	186: {";", ":"},
	187: {"=", "+"},
	188: {",", "<"},
	189: {"-", "_"},
	190: {".", ">"},
	191: {"/", "?"},
	192: {"`", "~"},
	219: {"[", "{"},
	220: {"\\", "|"},
	221: {"]", "}"},
	222: {"'", "\""},
}

// EvaluateKeyboardEvent translates a keydown event into the data to
// send to the pty.
func EvaluateKeyboardEvent(ev *KeyboardEvent, applicationCursorMode, isMac, macOptionIsMeta bool) KeyboardResult {
	result := KeyboardResult{Type: KeySendKey}
	modifiers := 0
	if ev.ShiftKey {
		modifiers |= 1
	}
	if ev.AltKey {
		modifiers |= 2
	}
	if ev.CtrlKey {
		modifiers |= 4
	}
	if ev.MetaKey {
		modifiers |= 8
	}

	// arrows in normal or application cursor mode
	arrow := func(final string) string {
		if modifiers != 0 {
			return c0ESC + "[1;" + itoa(modifiers+1) + final
		}
		if applicationCursorMode {
			return c0ESC + "O" + final
		}
		return c0ESC + "[" + final
	}
	fkeyTilde := func(num int) string {
		if modifiers != 0 {
			return c0ESC + "[" + itoa(num) + ";" + itoa(modifiers+1) + "~"
		}
		return c0ESC + "[" + itoa(num) + "~"
	}
	fkeySS3 := func(final string) string {
		if modifiers != 0 {
			return c0ESC + "[1;" + itoa(modifiers+1) + final
		}
		return c0ESC + "O" + final
	}

	switch ev.KeyCode {
	case 0:
		// iOS soft keyboard arrows
		app := map[string]string{
			"UIKeyInputUpArrow":    "A",
			"UIKeyInputDownArrow":  "B",
			"UIKeyInputRightArrow": "C",
			"UIKeyInputLeftArrow":  "D",
		}
		if final, ok := app[ev.Key]; ok {
			if applicationCursorMode {
				result.Key = c0ESC + "O" + final
			} else {
				result.Key = c0ESC + "[" + final
			}
		}
	case 8:
		// backspace: ^H or ^?
		if ev.CtrlKey {
			result.Key = "\b"
		} else {
			result.Key = c0DEL
		}
		if ev.AltKey {
			result.Key = c0ESC + result.Key
		}
	case 9:
		// tab
		if ev.ShiftKey {
			result.Key = c0ESC + "[Z"
			break
		}
		result.Key = "\t"
		result.Cancel = true
	case 13:
		// return/enter
		if ev.AltKey {
			result.Key = c0ESC + "\r"
		} else {
			result.Key = "\r"
		}
		result.Cancel = true
	case 27:
		// escape
		result.Key = c0ESC
		if ev.AltKey {
			result.Key = c0ESC + c0ESC
		}
		result.Cancel = true
	case 37: // left-arrow
		if ev.MetaKey {
			break
		}
		result.Key = arrow("D")
	case 39: // right-arrow
		if ev.MetaKey {
			break
		}
		result.Key = arrow("C")
	case 38: // up-arrow
		if ev.MetaKey {
			break
		}
		result.Key = arrow("A")
	case 40: // down-arrow
		if ev.MetaKey {
			break
		}
		result.Key = arrow("B")
	case 45:
		// insert; <Ctrl>/<Shift> + <Insert> are copy-paste on some
		// systems
		if !ev.ShiftKey && !ev.CtrlKey {
			result.Key = c0ESC + "[2~"
		}
	case 46: // delete
		result.Key = fkeyTilde(3)
	case 36: // home
		if modifiers != 0 {
			result.Key = c0ESC + "[1;" + itoa(modifiers+1) + "H"
		} else if applicationCursorMode {
			result.Key = c0ESC + "OH"
		} else {
			result.Key = c0ESC + "[H"
		}
	case 35: // end
		if modifiers != 0 {
			result.Key = c0ESC + "[1;" + itoa(modifiers+1) + "F"
		} else if applicationCursorMode {
			result.Key = c0ESC + "OF"
		} else {
			result.Key = c0ESC + "[F"
		}
	case 33: // page up
		if ev.ShiftKey {
			result.Type = KeyPageUp
		} else if ev.CtrlKey {
			result.Key = c0ESC + "[5;" + itoa(modifiers+1) + "~"
		} else {
			result.Key = c0ESC + "[5~"
		}
	case 34: // page down
		if ev.ShiftKey {
			result.Type = KeyPageDown
		} else if ev.CtrlKey {
			result.Key = c0ESC + "[6;" + itoa(modifiers+1) + "~"
		} else {
			result.Key = c0ESC + "[6~"
		}
	case 112: // F1
		result.Key = fkeySS3("P")
	case 113: // F2
		result.Key = fkeySS3("Q")
	case 114: // F3
		result.Key = fkeySS3("R")
	case 115: // F4
		result.Key = fkeySS3("S")
	case 116: // F5
		result.Key = fkeyTilde(15)
	case 117: // F6
		result.Key = fkeyTilde(17)
	case 118: // F7
		result.Key = fkeyTilde(18)
	case 119: // F8
		result.Key = fkeyTilde(19)
	case 120: // F9
		result.Key = fkeyTilde(20)
	case 121: // F10
		result.Key = fkeyTilde(21)
	case 122: // F11
		result.Key = fkeyTilde(23)
	case 123: // F12
		result.Key = fkeyTilde(24)
	default:
		// a-z and space
		if ev.CtrlKey && !ev.ShiftKey && !ev.AltKey && !ev.MetaKey {
			if ev.KeyCode >= 65 && ev.KeyCode <= 90 {
				result.Key = string(rune(ev.KeyCode - 64))
			} else if ev.KeyCode == 32 {
				result.Key = c0NUL
			} else if ev.KeyCode >= 51 && ev.KeyCode <= 55 {
				// escape, file sep, group sep, record sep, unit sep
				result.Key = string(rune(ev.KeyCode - 51 + 27))
			} else if ev.KeyCode == 56 {
				result.Key = c0DEL
			} else if ev.KeyCode == 219 {
				result.Key = c0ESC
			} else if ev.KeyCode == 220 {
				result.Key = c0FS
			} else if ev.KeyCode == 221 {
				result.Key = c0GS
			}
		} else if (!isMac || macOptionIsMeta) && ev.AltKey && !ev.MetaKey {
			// on macOS this is a third level shift when
			// !macOptionIsMeta; use <Esc> instead
			if mapping, ok := keycodeKeyMappings[ev.KeyCode]; ok {
				key := mapping[0]
				if ev.ShiftKey {
					key = mapping[1]
				}
				result.Key = c0ESC + key
			} else if ev.KeyCode >= 65 && ev.KeyCode <= 90 {
				keyCode := ev.KeyCode + 32
				if ev.CtrlKey {
					keyCode = ev.KeyCode - 64
				}
				keyString := string(rune(keyCode))
				if ev.ShiftKey {
					keyString = upperASCII(keyString)
				}
				result.Key = c0ESC + keyString
			} else if ev.KeyCode == 32 {
				if ev.CtrlKey {
					result.Key = c0ESC + c0NUL
				} else {
					result.Key = c0ESC + " "
				}
			} else if ev.Key == "Dead" && len(ev.Code) >= 4 && ev.Code[:3] == "Key" {
				// Alt can produce a "dead key" with some letters in US
				// layout (e.g. N/E/U); see xterm.js #3725
				keyString := ev.Code[3:4]
				if !ev.ShiftKey {
					keyString = lowerASCII(keyString)
				}
				result.Key = c0ESC + keyString
				result.Cancel = true
			}
		} else if isMac && !ev.AltKey && !ev.CtrlKey && !ev.ShiftKey && ev.MetaKey {
			if ev.KeyCode == 65 { // cmd + a
				result.Type = KeySelectAll
			}
		} else if ev.Key != "" && !ev.CtrlKey && !ev.AltKey && !ev.MetaKey && ev.KeyCode >= 48 && singleUTF16Unit(ev.Key) {
			// only keys resulting in a single character; no num lock,
			// volume up, etc.
			result.Key = ev.Key
		} else if ev.Key != "" && ev.CtrlKey {
			if ev.Key == "_" { // ^_
				result.Key = c0US
			}
			if ev.Key == "@" { // ^ + shift + 2 = ^ + @
				result.Key = c0NUL
			}
		}
	}

	return result
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [8]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}

func upperASCII(s string) string {
	b := []byte(s)
	for i := range b {
		if b[i] >= 'a' && b[i] <= 'z' {
			b[i] -= 32
		}
	}
	return string(b)
}

func lowerASCII(s string) string {
	b := []byte(s)
	for i := range b {
		if b[i] >= 'A' && b[i] <= 'Z' {
			b[i] += 32
		}
	}
	return string(b)
}

// singleUTF16Unit mirrors the JS `key.length === 1` check (UTF-16
// units, so astral chars are excluded like in the original).
func singleUTF16Unit(s string) bool {
	return len(utf16Units(s)) == 1
}
