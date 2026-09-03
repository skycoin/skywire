package canvas

import "github.com/gdamore/tcell/v3"

// PutRune writes one rune into a cell without the allocations that
// Screen.SetContent makes.
//
// SetContent is a shim in current tcell:
//
//	b.Put(x, y, string(append([]rune{mainc}, combc...)), style)
//
// so it allocates a rune slice and a string on every call, whatever the cell
// already held. A full-screen clear at 80x48 is 3,840 cells, and therefore
// 3,840 allocations a frame before anything is drawn — measured, on every
// animation here that talks to the screen directly rather than through a
// Surface. Put takes the string, so a cached one costs nothing.
//
// The table covers what these animations draw: printable ASCII, box drawing,
// block elements, geometric shapes, braille and half-width katakana. Anything
// outside it still works, at the price of the string conversion, which is then
// the rare case rather than every cell.
func PutRune(screen tcell.Screen, x, y int, r rune, st tcell.Style) {
	if s, ok := runeStr[r]; ok {
		screen.Put(x, y, s, st) //nolint:errcheck // no error is possible for one cell
		return
	}
	screen.Put(x, y, string(r), st) //nolint:errcheck // as above
}

// Blank is the space, as a string, for the clear loops that dominate the count.
const Blank = " "

// runeStr is filled once at init and only read afterwards, so it is safe to
// share between animations running concurrently.
var runeStr = func() map[rune]string {
	m := make(map[rune]string, 1024)
	add := func(lo, hi rune) {
		for r := lo; r <= hi; r++ {
			m[r] = string(r)
		}
	}
	add(0x20, 0x7e)     // printable ASCII
	add(0x2500, 0x257f) // box drawing
	add(0x2580, 0x259f) // block elements
	add(0x25a0, 0x25ff) // geometric shapes
	add(0x2800, 0x28ff) // braille
	add(0xff61, 0xff9f) // half-width katakana, which is what the matrix rain is made of
	return m
}()
