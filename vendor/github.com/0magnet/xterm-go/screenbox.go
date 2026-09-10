package xterm

// screenBoxPx is the size of the terminal's screen box in CSS pixels.
//
// One expression with two users: .xterm-screen is laid out with it, and the
// WebGL canvas is sized with it so that it overlays the screen exactly. They
// used to be computed separately, and separately is how they drifted — the
// canvas derived its box by dividing the ceil-ed DEVICE size back down by the
// pixel ratio, which does not return the height it started from — ceil adds up
// to a pixel per row at ANY pixel ratio, and more at a fractional one. On a 0.8125
// dpr display at 46 rows that produced a 951px canvas over a 906px screen, and
// the 41px difference hung out of the bottom of the terminal, putting a
// scrollbar on a window whose content fitted.
//
// It lives in a file with no build tag so the arithmetic can be tested without
// a browser, which is the whole of what went wrong.
func screenBoxPx(cellW, cellH float64, cols, rows int) (w, h float64) {
	return cellW * float64(cols), cellH * float64(rows)
}
