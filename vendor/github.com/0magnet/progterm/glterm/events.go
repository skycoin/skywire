package glterm

// The terminal's own input events. A window layer (Gio, on the desktop and
// the phone) translates its events into these, so nothing here depends on
// which one it is.

// Mods is the modifier keys held.
type Mods uint8

// The modifiers.
const (
	Shift Mods = 1 << iota
	Ctrl
	Alt
	Meta
)

// Key is a key that is not a character.
type Key int

// The named keys.
const (
	KeyNone Key = iota
	KeyEnter
	KeyEscape
	KeyBackspace
	KeyTab
	KeyInsert
	KeyDelete
	KeyHome
	KeyEnd
	KeyPageUp
	KeyPageDown
	KeyLeft
	KeyUp
	KeyRight
	KeyDown
	KeyF1
	KeyF2
	KeyF3
	KeyF4
	KeyF5
	KeyF6
	KeyF7
	KeyF8
	KeyF9
	KeyF10
	KeyF11
	KeyF12
)

// KeyEvent is a key press: a named Key, or a Rune with Key left KeyNone.
// Typed text without Ctrl or Alt comes as Terminal.Text instead.
type KeyEvent struct {
	Key  Key
	Rune rune
	Mods Mods
}

// Button is a mouse button, or a step of the wheel.
type Button int

// The buttons.
const (
	ButtonNone Button = iota
	ButtonLeft
	ButtonMiddle
	ButtonRight
	WheelUp
	WheelDown
)

// Action is what a mouse event is.
type Action int

// The actions. A wheel step is a Press of WheelUp or WheelDown.
const (
	Press Action = iota
	Release
	Move
)

// MouseEvent is the pointer at (X, Y), in pixels.
type MouseEvent struct {
	X, Y   float32
	Button Button
	Action Action
	Mods   Mods
}
