//go:build js && wasm

package xterm

// Port of addons/addon-webgl/src/CustomGlyphs.ts — pixel-perfect
// procedural rendering of block elements, shade patterns, box drawing
// and powerline glyphs, instead of relying on font glyphs which often
// have gaps or are missing entirely.

import (
	"fmt"
	"strconv"
	"strings"
	"syscall/js"
)

type blockVector struct{ x, y, w, h int }

var blockElementDefinitions = map[string][]blockVector{
	// Block elements (0x2580-0x2590)
	"▀": {{0, 0, 8, 4}}, // UPPER HALF BLOCK
	"▁": {{0, 7, 8, 1}}, // LOWER ONE EIGHTH BLOCK
	"▂": {{0, 6, 8, 2}}, // LOWER ONE QUARTER BLOCK
	"▃": {{0, 5, 8, 3}}, // LOWER THREE EIGHTHS BLOCK
	"▄": {{0, 4, 8, 4}}, // LOWER HALF BLOCK
	"▅": {{0, 3, 8, 5}}, // LOWER FIVE EIGHTHS BLOCK
	"▆": {{0, 2, 8, 6}}, // LOWER THREE QUARTERS BLOCK
	"▇": {{0, 1, 8, 7}}, // LOWER SEVEN EIGHTHS BLOCK
	"█": {{0, 0, 8, 8}}, // FULL BLOCK
	"▉": {{0, 0, 7, 8}}, // LEFT SEVEN EIGHTHS BLOCK
	"▊": {{0, 0, 6, 8}}, // LEFT THREE QUARTERS BLOCK
	"▋": {{0, 0, 5, 8}}, // LEFT FIVE EIGHTHS BLOCK
	"▌": {{0, 0, 4, 8}}, // LEFT HALF BLOCK
	"▍": {{0, 0, 3, 8}}, // LEFT THREE EIGHTHS BLOCK
	"▎": {{0, 0, 2, 8}}, // LEFT ONE QUARTER BLOCK
	"▏": {{0, 0, 1, 8}}, // LEFT ONE EIGHTH BLOCK
	"▐": {{4, 0, 4, 8}}, // RIGHT HALF BLOCK

	// Block elements (0x2594-0x2595)
	"▔": {{0, 0, 8, 1}}, // UPPER ONE EIGHTH BLOCK
	"▕": {{7, 0, 1, 8}}, // RIGHT ONE EIGHTH BLOCK

	// Terminal graphic characters (0x2596-0x259F)
	"▖": {{0, 4, 4, 4}},
	"▗": {{4, 4, 4, 4}},
	"▘": {{0, 0, 4, 4}},
	"▙": {{0, 0, 4, 8}, {0, 4, 8, 4}},
	"▚": {{0, 0, 4, 4}, {4, 4, 4, 4}},
	"▛": {{0, 0, 4, 8}, {4, 0, 4, 4}},
	"▜": {{0, 0, 8, 4}, {4, 0, 4, 8}},
	"▝": {{4, 0, 4, 4}},
	"▞": {{4, 0, 4, 4}, {0, 4, 4, 4}},
	"▟": {{4, 0, 4, 8}, {0, 4, 8, 4}},

	// VERTICAL ONE EIGHTH BLOCK-2 through -7
	"\U0001FB70": {{1, 0, 1, 8}},
	"\U0001FB71": {{2, 0, 1, 8}},
	"\U0001FB72": {{3, 0, 1, 8}},
	"\U0001FB73": {{4, 0, 1, 8}},
	"\U0001FB74": {{5, 0, 1, 8}},
	"\U0001FB75": {{6, 0, 1, 8}},

	// HORIZONTAL ONE EIGHTH BLOCK-2 through -7
	"\U0001FB76": {{0, 1, 8, 1}},
	"\U0001FB77": {{0, 2, 8, 1}},
	"\U0001FB78": {{0, 3, 8, 1}},
	"\U0001FB79": {{0, 4, 8, 1}},
	"\U0001FB7A": {{0, 5, 8, 1}},
	"\U0001FB7B": {{0, 6, 8, 1}},

	"\U0001FB7C": {{0, 0, 1, 8}, {0, 7, 8, 1}},
	"\U0001FB7D": {{0, 0, 1, 8}, {0, 0, 8, 1}},
	"\U0001FB7E": {{7, 0, 1, 8}, {0, 0, 8, 1}},
	"\U0001FB7F": {{7, 0, 1, 8}, {0, 7, 8, 1}},
	"\U0001FB80": {{0, 0, 8, 1}, {0, 7, 8, 1}},
	"\U0001FB81": {{0, 0, 8, 1}, {0, 2, 8, 1}, {0, 4, 8, 1}, {0, 7, 8, 1}},

	"\U0001FB82": {{0, 0, 8, 2}},
	"\U0001FB83": {{0, 0, 8, 3}},
	"\U0001FB84": {{0, 0, 8, 5}},
	"\U0001FB85": {{0, 0, 8, 6}},
	"\U0001FB86": {{0, 0, 8, 7}},

	"\U0001FB87": {{6, 0, 2, 8}},
	"\U0001FB88": {{5, 0, 3, 8}},
	"\U0001FB89": {{3, 0, 5, 8}},
	"\U0001FB8A": {{2, 0, 6, 8}},
	"\U0001FB8B": {{1, 0, 7, 8}},

	// CHECKER BOARD FILL
	"\U0001FB95": {
		{0, 0, 2, 2}, {4, 0, 2, 2},
		{2, 2, 2, 2}, {6, 2, 2, 2},
		{0, 4, 2, 2}, {4, 4, 2, 2},
		{2, 6, 2, 2}, {6, 6, 2, 2},
	},
	// INVERSE CHECKER BOARD FILL
	"\U0001FB96": {
		{2, 0, 2, 2}, {6, 0, 2, 2},
		{0, 2, 2, 2}, {4, 2, 2, 2},
		{2, 4, 2, 2}, {6, 4, 2, 2},
		{0, 6, 2, 2}, {4, 6, 2, 2},
	},
	// HEAVY HORIZONTAL FILL
	"\U0001FB97": {{0, 2, 8, 2}, {0, 6, 8, 2}},
}

// Shade characters: repeating pixel patterns (1 = filled).
var patternCharacterDefinitions = map[string][][]int{
	"░": { // LIGHT SHADE (25%)
		{1, 0, 0, 0},
		{0, 0, 0, 0},
		{0, 0, 1, 0},
		{0, 0, 0, 0},
	},
	"▒": { // MEDIUM SHADE (50%)
		{1, 0},
		{0, 0},
		{0, 1},
		{0, 0},
	},
	"▓": { // DARK SHADE (75%)
		{0, 1},
		{1, 1},
		{1, 0},
		{1, 1},
	},
}

// Line shapes as SVG-style path strings (the d attribute subset M/L/C).
const (
	shpTopToBottom    = "M.5,0 L.5,1"
	shpLeftToRight    = "M0,.5 L1,.5"
	shpTopToRight     = "M.5,0 L.5,.5 L1,.5"
	shpTopToLeft      = "M.5,0 L.5,.5 L0,.5"
	shpLeftToBottom   = "M0,.5 L.5,.5 L.5,1"
	shpRightToBottom  = "M0.5,1 L.5,.5 L1,.5"
	shpMiddleToTop    = "M.5,.5 L.5,0"
	shpMiddleToLeft   = "M.5,.5 L0,.5"
	shpMiddleToRight  = "M.5,.5 L1,.5"
	shpMiddleToBottom = "M.5,.5 L.5,1"
	shpTTop           = "M0,.5 L1,.5 M.5,.5 L.5,0"
	shpTLeft          = "M.5,0 L.5,1 M.5,.5 L0,.5"
	shpTRight         = "M.5,0 L.5,1 M.5,.5 L1,.5"
	shpTBottom        = "M0,.5 L1,.5 M.5,.5 L.5,1"
	shpCross          = "M0,.5 L1,.5 M.5,0 L.5,1"
	shpTwoDashesH     = "M.1,.5 L.4,.5 M.6,.5 L.9,.5"
	shpThreeDashesH   = "M.0667,.5 L.2667,.5 M.4,.5 L.6,.5 M.7333,.5 L.9333,.5"
	shpFourDashesH    = "M.05,.5 L.2,.5 M.3,.5 L.45,.5 M.55,.5 L.7,.5 M.8,.5 L.95,.5"
	shpTwoDashesV     = "M.5,.1 L.5,.4 M.5,.6 L.5,.9"
	shpThreeDashesV   = "M.5,.0667 L.5,.2667 M.5,.4 L.5,.6 M.5,.7333 L.5,.9333"
	shpFourDashesV    = "M.5,.05 L.5,.2 M.5,.3 L.5,.45 L.5,.55 M.5,.7 L.5,.95"
)

const (
	styleNormal = 1
	styleBold   = 3
)

// boxEntry is one weight variant of a box drawing char: either a fixed
// path or a function of the double-line offsets (xp, yp).
type boxEntry struct {
	weight int
	path   string
	fn     func(xp, yp float64) string
}

func f(v float64) string { return strconv.FormatFloat(v, 'g', -1, 64) }

var boxDrawingDefinitions = map[string][]boxEntry{
	// Uniform normal and bold
	"─": {{styleNormal, shpLeftToRight, nil}},
	"━": {{styleBold, shpLeftToRight, nil}},
	"│": {{styleNormal, shpTopToBottom, nil}},
	"┃": {{styleBold, shpTopToBottom, nil}},
	"┌": {{styleNormal, shpRightToBottom, nil}},
	"┏": {{styleBold, shpRightToBottom, nil}},
	"┐": {{styleNormal, shpLeftToBottom, nil}},
	"┓": {{styleBold, shpLeftToBottom, nil}},
	"└": {{styleNormal, shpTopToRight, nil}},
	"┗": {{styleBold, shpTopToRight, nil}},
	"┘": {{styleNormal, shpTopToLeft, nil}},
	"┛": {{styleBold, shpTopToLeft, nil}},
	"├": {{styleNormal, shpTRight, nil}},
	"┣": {{styleBold, shpTRight, nil}},
	"┤": {{styleNormal, shpTLeft, nil}},
	"┫": {{styleBold, shpTLeft, nil}},
	"┬": {{styleNormal, shpTBottom, nil}},
	"┳": {{styleBold, shpTBottom, nil}},
	"┴": {{styleNormal, shpTTop, nil}},
	"┻": {{styleBold, shpTTop, nil}},
	"┼": {{styleNormal, shpCross, nil}},
	"╋": {{styleBold, shpCross, nil}},
	"╴": {{styleNormal, shpMiddleToLeft, nil}},
	"╸": {{styleBold, shpMiddleToLeft, nil}},
	"╵": {{styleNormal, shpMiddleToTop, nil}},
	"╹": {{styleBold, shpMiddleToTop, nil}},
	"╶": {{styleNormal, shpMiddleToRight, nil}},
	"╺": {{styleBold, shpMiddleToRight, nil}},
	"╷": {{styleNormal, shpMiddleToBottom, nil}},
	"╻": {{styleBold, shpMiddleToBottom, nil}},

	// Double border
	"═": {{styleNormal, "", func(xp, yp float64) string {
		return "M0," + f(.5-yp) + " L1," + f(.5-yp) + " M0," + f(.5+yp) + " L1," + f(.5+yp)
	}}},
	"║": {{styleNormal, "", func(xp, yp float64) string {
		return "M" + f(.5-xp) + ",0 L" + f(.5-xp) + ",1 M" + f(.5+xp) + ",0 L" + f(.5+xp) + ",1"
	}}},
	"╒": {{styleNormal, "", func(xp, yp float64) string {
		return "M.5,1 L.5," + f(.5-yp) + " L1," + f(.5-yp) + " M.5," + f(.5+yp) + " L1," + f(.5+yp)
	}}},
	"╓": {{styleNormal, "", func(xp, yp float64) string {
		return "M" + f(.5-xp) + ",1 L" + f(.5-xp) + ",.5 L1,.5 M" + f(.5+xp) + ",.5 L" + f(.5+xp) + ",1"
	}}},
	"╔": {{styleNormal, "", func(xp, yp float64) string {
		return "M1," + f(.5-yp) + " L" + f(.5-xp) + "," + f(.5-yp) + " L" + f(.5-xp) + ",1 M1," + f(.5+yp) + " L" + f(.5+xp) + "," + f(.5+yp) + " L" + f(.5+xp) + ",1"
	}}},
	"╕": {{styleNormal, "", func(xp, yp float64) string {
		return "M0," + f(.5-yp) + " L.5," + f(.5-yp) + " L.5,1 M0," + f(.5+yp) + " L.5," + f(.5+yp)
	}}},
	"╖": {{styleNormal, "", func(xp, yp float64) string {
		return "M" + f(.5+xp) + ",1 L" + f(.5+xp) + ",.5 L0,.5 M" + f(.5-xp) + ",.5 L" + f(.5-xp) + ",1"
	}}},
	"╗": {{styleNormal, "", func(xp, yp float64) string {
		return "M0," + f(.5+yp) + " L" + f(.5-xp) + "," + f(.5+yp) + " L" + f(.5-xp) + ",1 M0," + f(.5-yp) + " L" + f(.5+xp) + "," + f(.5-yp) + " L" + f(.5+xp) + ",1"
	}}},
	"╘": {{styleNormal, "", func(xp, yp float64) string {
		return "M.5,0 L.5," + f(.5+yp) + " L1," + f(.5+yp) + " M.5," + f(.5-yp) + " L1," + f(.5-yp)
	}}},
	"╙": {{styleNormal, "", func(xp, yp float64) string {
		return "M1,.5 L" + f(.5-xp) + ",.5 L" + f(.5-xp) + ",0 M" + f(.5+xp) + ",.5 L" + f(.5+xp) + ",0"
	}}},
	"╚": {{styleNormal, "", func(xp, yp float64) string {
		return "M1," + f(.5-yp) + " L" + f(.5+xp) + "," + f(.5-yp) + " L" + f(.5+xp) + ",0 M1," + f(.5+yp) + " L" + f(.5-xp) + "," + f(.5+yp) + " L" + f(.5-xp) + ",0"
	}}},
	"╛": {{styleNormal, "", func(xp, yp float64) string {
		return "M0," + f(.5+yp) + " L.5," + f(.5+yp) + " L.5,0 M0," + f(.5-yp) + " L.5," + f(.5-yp)
	}}},
	"╜": {{styleNormal, "", func(xp, yp float64) string {
		return "M0,.5 L" + f(.5+xp) + ",.5 L" + f(.5+xp) + ",0 M" + f(.5-xp) + ",.5 L" + f(.5-xp) + ",0"
	}}},
	"╝": {{styleNormal, "", func(xp, yp float64) string {
		return "M0," + f(.5-yp) + " L" + f(.5-xp) + "," + f(.5-yp) + " L" + f(.5-xp) + ",0 M0," + f(.5+yp) + " L" + f(.5+xp) + "," + f(.5+yp) + " L" + f(.5+xp) + ",0"
	}}},
	"╞": {{styleNormal, "", func(xp, yp float64) string {
		return shpTopToBottom + " M.5," + f(.5-yp) + " L1," + f(.5-yp) + " M.5," + f(.5+yp) + " L1," + f(.5+yp)
	}}},
	"╟": {{styleNormal, "", func(xp, yp float64) string {
		return "M" + f(.5-xp) + ",0 L" + f(.5-xp) + ",1 M" + f(.5+xp) + ",0 L" + f(.5+xp) + ",1 M" + f(.5+xp) + ",.5 L1,.5"
	}}},
	"╠": {{styleNormal, "", func(xp, yp float64) string {
		return "M" + f(.5-xp) + ",0 L" + f(.5-xp) + ",1 M1," + f(.5+yp) + " L" + f(.5+xp) + "," + f(.5+yp) + " L" + f(.5+xp) + ",1 M1," + f(.5-yp) + " L" + f(.5+xp) + "," + f(.5-yp) + " L" + f(.5+xp) + ",0"
	}}},
	"╡": {{styleNormal, "", func(xp, yp float64) string {
		return shpTopToBottom + " M0," + f(.5-yp) + " L.5," + f(.5-yp) + " M0," + f(.5+yp) + " L.5," + f(.5+yp)
	}}},
	"╢": {{styleNormal, "", func(xp, yp float64) string {
		return "M0,.5 L" + f(.5-xp) + ",.5 M" + f(.5-xp) + ",0 L" + f(.5-xp) + ",1 M" + f(.5+xp) + ",0 L" + f(.5+xp) + ",1"
	}}},
	"╣": {{styleNormal, "", func(xp, yp float64) string {
		return "M" + f(.5+xp) + ",0 L" + f(.5+xp) + ",1 M0," + f(.5+yp) + " L" + f(.5-xp) + "," + f(.5+yp) + " L" + f(.5-xp) + ",1 M0," + f(.5-yp) + " L" + f(.5-xp) + "," + f(.5-yp) + " L" + f(.5-xp) + ",0"
	}}},
	"╤": {{styleNormal, "", func(xp, yp float64) string {
		return "M0," + f(.5-yp) + " L1," + f(.5-yp) + " M0," + f(.5+yp) + " L1," + f(.5+yp) + " M.5," + f(.5+yp) + " L.5,1"
	}}},
	"╥": {{styleNormal, "", func(xp, yp float64) string {
		return shpLeftToRight + " M" + f(.5-xp) + ",.5 L" + f(.5-xp) + ",1 M" + f(.5+xp) + ",.5 L" + f(.5+xp) + ",1"
	}}},
	"╦": {{styleNormal, "", func(xp, yp float64) string {
		return "M0," + f(.5-yp) + " L1," + f(.5-yp) + " M0," + f(.5+yp) + " L" + f(.5-xp) + "," + f(.5+yp) + " L" + f(.5-xp) + ",1 M1," + f(.5+yp) + " L" + f(.5+xp) + "," + f(.5+yp) + " L" + f(.5+xp) + ",1"
	}}},
	"╧": {{styleNormal, "", func(xp, yp float64) string {
		return "M.5,0 L.5," + f(.5-yp) + " M0," + f(.5-yp) + " L1," + f(.5-yp) + " M0," + f(.5+yp) + " L1," + f(.5+yp)
	}}},
	"╨": {{styleNormal, "", func(xp, yp float64) string {
		return shpLeftToRight + " M" + f(.5-xp) + ",.5 L" + f(.5-xp) + ",0 M" + f(.5+xp) + ",.5 L" + f(.5+xp) + ",0"
	}}},
	"╩": {{styleNormal, "", func(xp, yp float64) string {
		return "M0," + f(.5+yp) + " L1," + f(.5+yp) + " M0," + f(.5-yp) + " L" + f(.5-xp) + "," + f(.5-yp) + " L" + f(.5-xp) + ",0 M1," + f(.5-yp) + " L" + f(.5+xp) + "," + f(.5-yp) + " L" + f(.5+xp) + ",0"
	}}},
	"╪": {{styleNormal, "", func(xp, yp float64) string {
		return shpTopToBottom + " M0," + f(.5-yp) + " L1," + f(.5-yp) + " M0," + f(.5+yp) + " L1," + f(.5+yp)
	}}},
	"╫": {{styleNormal, "", func(xp, yp float64) string {
		return shpLeftToRight + " M" + f(.5-xp) + ",0 L" + f(.5-xp) + ",1 M" + f(.5+xp) + ",0 L" + f(.5+xp) + ",1"
	}}},
	"╬": {{styleNormal, "", func(xp, yp float64) string {
		return "M0," + f(.5+yp) + " L" + f(.5-xp) + "," + f(.5+yp) + " L" + f(.5-xp) + ",1 M1," + f(.5+yp) + " L" + f(.5+xp) + "," + f(.5+yp) + " L" + f(.5+xp) + ",1 M0," + f(.5-yp) + " L" + f(.5-xp) + "," + f(.5-yp) + " L" + f(.5-xp) + ",0 M1," + f(.5-yp) + " L" + f(.5+xp) + "," + f(.5-yp) + " L" + f(.5+xp) + ",0"
	}}},

	// Diagonal
	"╱": {{styleNormal, "M1,0 L0,1", nil}},
	"╲": {{styleNormal, "M0,0 L1,1", nil}},
	"╳": {{styleNormal, "M1,0 L0,1 M0,0 L1,1", nil}},

	// Mixed weight
	"╼": {{styleNormal, shpMiddleToLeft, nil}, {styleBold, shpMiddleToRight, nil}},
	"╽": {{styleNormal, shpMiddleToTop, nil}, {styleBold, shpMiddleToBottom, nil}},
	"╾": {{styleNormal, shpMiddleToRight, nil}, {styleBold, shpMiddleToLeft, nil}},
	"╿": {{styleNormal, shpMiddleToBottom, nil}, {styleBold, shpMiddleToTop, nil}},
	"┍": {{styleNormal, shpMiddleToBottom, nil}, {styleBold, shpMiddleToRight, nil}},
	"┎": {{styleNormal, shpMiddleToRight, nil}, {styleBold, shpMiddleToBottom, nil}},
	"┑": {{styleNormal, shpMiddleToBottom, nil}, {styleBold, shpMiddleToLeft, nil}},
	"┒": {{styleNormal, shpMiddleToLeft, nil}, {styleBold, shpMiddleToBottom, nil}},
	"┕": {{styleNormal, shpMiddleToTop, nil}, {styleBold, shpMiddleToRight, nil}},
	"┖": {{styleNormal, shpMiddleToRight, nil}, {styleBold, shpMiddleToTop, nil}},
	"┙": {{styleNormal, shpMiddleToTop, nil}, {styleBold, shpMiddleToLeft, nil}},
	"┚": {{styleNormal, shpMiddleToLeft, nil}, {styleBold, shpMiddleToTop, nil}},
	"┝": {{styleNormal, shpTopToBottom, nil}, {styleBold, shpMiddleToRight, nil}},
	"┞": {{styleNormal, shpRightToBottom, nil}, {styleBold, shpMiddleToTop, nil}},
	"┟": {{styleNormal, shpTopToRight, nil}, {styleBold, shpMiddleToBottom, nil}},
	"┠": {{styleNormal, shpMiddleToRight, nil}, {styleBold, shpTopToBottom, nil}},
	"┡": {{styleNormal, shpMiddleToBottom, nil}, {styleBold, shpTopToRight, nil}},
	"┢": {{styleNormal, shpMiddleToTop, nil}, {styleBold, shpRightToBottom, nil}},
	"┥": {{styleNormal, shpTopToBottom, nil}, {styleBold, shpMiddleToLeft, nil}},
	"┦": {{styleNormal, shpLeftToBottom, nil}, {styleBold, shpMiddleToTop, nil}},
	"┧": {{styleNormal, shpTopToLeft, nil}, {styleBold, shpMiddleToBottom, nil}},
	"┨": {{styleNormal, shpMiddleToLeft, nil}, {styleBold, shpTopToBottom, nil}},
	"┩": {{styleNormal, shpMiddleToBottom, nil}, {styleBold, shpTopToLeft, nil}},
	"┪": {{styleNormal, shpMiddleToTop, nil}, {styleBold, shpLeftToBottom, nil}},
	"┭": {{styleNormal, shpRightToBottom, nil}, {styleBold, shpMiddleToLeft, nil}},
	"┮": {{styleNormal, shpLeftToBottom, nil}, {styleBold, shpMiddleToRight, nil}},
	"┯": {{styleNormal, shpMiddleToBottom, nil}, {styleBold, shpLeftToRight, nil}},
	"┰": {{styleNormal, shpLeftToRight, nil}, {styleBold, shpMiddleToBottom, nil}},
	"┱": {{styleNormal, shpMiddleToRight, nil}, {styleBold, shpLeftToBottom, nil}},
	"┲": {{styleNormal, shpMiddleToLeft, nil}, {styleBold, shpRightToBottom, nil}},
	"┵": {{styleNormal, shpTopToRight, nil}, {styleBold, shpMiddleToLeft, nil}},
	"┶": {{styleNormal, shpTopToLeft, nil}, {styleBold, shpMiddleToRight, nil}},
	"┷": {{styleNormal, shpMiddleToTop, nil}, {styleBold, shpLeftToRight, nil}},
	"┸": {{styleNormal, shpLeftToRight, nil}, {styleBold, shpMiddleToTop, nil}},
	"┹": {{styleNormal, shpMiddleToRight, nil}, {styleBold, shpTopToLeft, nil}},
	"┺": {{styleNormal, shpMiddleToLeft, nil}, {styleBold, shpTopToRight, nil}},
	"┽": {{styleNormal, shpTopToBottom + " " + shpMiddleToRight, nil}, {styleBold, shpMiddleToLeft, nil}},
	"┾": {{styleNormal, shpTopToBottom + " " + shpMiddleToLeft, nil}, {styleBold, shpMiddleToRight, nil}},
	"┿": {{styleNormal, shpTopToBottom, nil}, {styleBold, shpLeftToRight, nil}},
	"╀": {{styleNormal, shpLeftToRight + " " + shpMiddleToBottom, nil}, {styleBold, shpMiddleToTop, nil}},
	"╁": {{styleNormal, shpMiddleToTop + " " + shpLeftToRight, nil}, {styleBold, shpMiddleToBottom, nil}},
	"╂": {{styleNormal, shpLeftToRight, nil}, {styleBold, shpTopToBottom, nil}},
	"╃": {{styleNormal, shpRightToBottom, nil}, {styleBold, shpTopToLeft, nil}},
	"╄": {{styleNormal, shpLeftToBottom, nil}, {styleBold, shpTopToRight, nil}},
	"╅": {{styleNormal, shpTopToRight, nil}, {styleBold, shpLeftToBottom, nil}},
	"╆": {{styleNormal, shpTopToLeft, nil}, {styleBold, shpRightToBottom, nil}},
	"╇": {{styleNormal, shpMiddleToBottom, nil}, {styleBold, shpMiddleToTop + " " + shpLeftToRight, nil}},
	"╈": {{styleNormal, shpMiddleToTop, nil}, {styleBold, shpLeftToRight + " " + shpMiddleToBottom, nil}},
	"╉": {{styleNormal, shpMiddleToRight, nil}, {styleBold, shpTopToBottom + " " + shpMiddleToLeft, nil}},
	"╊": {{styleNormal, shpMiddleToLeft, nil}, {styleBold, shpTopToBottom + " " + shpMiddleToRight, nil}},

	// Dashed
	"╌": {{styleNormal, shpTwoDashesH, nil}},
	"╍": {{styleBold, shpTwoDashesH, nil}},
	"┄": {{styleNormal, shpThreeDashesH, nil}},
	"┅": {{styleBold, shpThreeDashesH, nil}},
	"┈": {{styleNormal, shpFourDashesH, nil}},
	"┉": {{styleBold, shpFourDashesH, nil}},
	"╎": {{styleNormal, shpTwoDashesV, nil}},
	"╏": {{styleBold, shpTwoDashesV, nil}},
	"┆": {{styleNormal, shpThreeDashesV, nil}},
	"┇": {{styleBold, shpThreeDashesV, nil}},
	"┊": {{styleNormal, shpFourDashesV, nil}},
	"┋": {{styleBold, shpFourDashesV, nil}},

	// Curved
	"╭": {{styleNormal, "", func(xp, yp float64) string {
		return "M.5,1 L.5," + f(.5+(yp/.15*.5)) + " C.5," + f(.5+(yp/.15*.5)) + ",.5,.5,1,.5"
	}}},
	"╮": {{styleNormal, "", func(xp, yp float64) string {
		return "M.5,1 L.5," + f(.5+(yp/.15*.5)) + " C.5," + f(.5+(yp/.15*.5)) + ",.5,.5,0,.5"
	}}},
	"╯": {{styleNormal, "", func(xp, yp float64) string {
		return "M.5,0 L.5," + f(.5-(yp/.15*.5)) + " C.5," + f(.5-(yp/.15*.5)) + ",.5,.5,0,.5"
	}}},
	"╰": {{styleNormal, "", func(xp, yp float64) string {
		return "M.5,0 L.5," + f(.5-(yp/.15*.5)) + " C.5," + f(.5-(yp/.15*.5)) + ",.5,.5,1,.5"
	}}},
}

const (
	vectorFill = iota
	vectorStroke
)

type vectorShape struct {
	d            string
	typ          int
	leftPadding  float64
	rightPadding float64
}

// Powerline symbols as vector shapes (original symbols defined in
// https://github.com/powerline/fontpatcher).
var powerlineDefinitions = map[string]vectorShape{
	// Git branch
	"\uE0A0": {d: "M.3,1 L.03,1 L.03,.88 C.03,.82,.06,.78,.11,.73 C.15,.7,.2,.68,.28,.65 L.43,.6 C.49,.58,.53,.56,.56,.53 C.59,.5,.6,.47,.6,.43 L.6,.27 L.4,.27 L.69,.1 L.98,.27 L.78,.27 L.78,.46 C.78,.52,.76,.56,.72,.61 C.68,.66,.63,.67,.56,.7 L.48,.72 C.42,.74,.38,.76,.35,.78 C.32,.8,.31,.84,.31,.88 L.31,1 M.3,.5 L.03,.59 L.03,.09 L.3,.09 L.3,.655", typ: vectorFill},
	// L N
	"\uE0A1": {d: "M.7,.4 L.7,.47 L.2,.47 L.2,.03 L.355,.03 L.355,.4 L.705,.4 M.7,.5 L.86,.5 L.86,.95 L.69,.95 L.44,.66 L.46,.86 L.46,.95 L.3,.95 L.3,.49 L.46,.49 L.71,.78 L.69,.565 L.69,.5", typ: vectorFill},
	// Lock
	"\uE0A2": {d: "M.25,.94 C.16,.94,.11,.92,.11,.87 L.11,.53 C.11,.48,.15,.455,.23,.45 L.23,.3 C.23,.25,.26,.22,.31,.19 C.36,.16,.43,.15,.51,.15 C.59,.15,.66,.16,.71,.19 C.77,.22,.79,.26,.79,.3 L.79,.45 C.87,.45,.91,.48,.91,.53 L.91,.87 C.91,.92,.86,.94,.77,.94 L.24,.94 M.53,.2 C.49,.2,.45,.21,.42,.23 C.39,.25,.38,.27,.38,.3 L.38,.45 L.68,.45 L.68,.3 C.68,.27,.67,.25,.64,.23 C.61,.21,.58,.2,.53,.2 M.58,.82 L.58,.66 C.63,.65,.65,.63,.65,.6 C.65,.58,.64,.57,.61,.56 C.58,.55,.56,.54,.52,.54 C.48,.54,.46,.55,.43,.56 C.4,.57,.39,.59,.39,.6 C.39,.63,.41,.64,.46,.66 L.46,.82 L.57,.82", typ: vectorFill},
	// Right triangle solid
	"\uE0B0": {d: "M0,0 L1,.5 L0,1", typ: vectorFill, rightPadding: 2},
	// Right triangle line
	"\uE0B1": {d: "M-1,-.5 L1,.5 L-1,1.5", typ: vectorStroke, leftPadding: 1, rightPadding: 1},
	// Left triangle solid
	"\uE0B2": {d: "M1,0 L0,.5 L1,1", typ: vectorFill, leftPadding: 2},
	// Left triangle line
	"\uE0B3": {d: "M2,-.5 L0,.5 L2,1.5", typ: vectorStroke, leftPadding: 1, rightPadding: 1},
	// Right semi-circle solid
	"\uE0B4": {d: "M0,0 L0,1 C0.552,1,1,0.776,1,.5 C1,0.224,0.552,0,0,0", typ: vectorFill, rightPadding: 1},
	// Right semi-circle line
	"\uE0B5": {d: "M.2,1 C.422,1,.8,.826,.78,.5 C.8,.174,0.422,0,.2,0", typ: vectorStroke, rightPadding: 1},
	// Left semi-circle solid
	"\uE0B6": {d: "M1,0 L1,1 C0.448,1,0,0.776,0,.5 C0,0.224,0.448,0,1,0", typ: vectorFill, leftPadding: 1},
	// Left semi-circle line
	"\uE0B7": {d: "M.8,1 C0.578,1,0.2,.826,.22,.5 C0.2,0.174,0.578,0,0.8,0", typ: vectorStroke, leftPadding: 1},
	// Lower left triangle
	"\uE0B8": {d: "M-.5,-.5 L1.5,1.5 L-.5,1.5", typ: vectorFill},
	// Backslash separator
	"\uE0B9": {d: "M-.5,-.5 L1.5,1.5", typ: vectorStroke, leftPadding: 1, rightPadding: 1},
	// Lower right triangle
	"\uE0BA": {d: "M1.5,-.5 L-.5,1.5 L1.5,1.5", typ: vectorFill},
	// Upper left triangle
	"\uE0BC": {d: "M1.5,-.5 L-.5,1.5 L-.5,-.5", typ: vectorFill},
	// Forward slash separator
	"\uE0BD": {d: "M1.5,-.5 L-.5,1.5", typ: vectorStroke, leftPadding: 1, rightPadding: 1},
	// Upper right triangle
	"\uE0BE": {d: "M-.5,-.5 L1.5,1.5 L1.5,-.5", typ: vectorFill},
}

func init() {
	// redundant separators
	powerlineDefinitions[""] = powerlineDefinitions[""]
	powerlineDefinitions[""] = powerlineDefinitions[""]
}

// tryDrawCustomChar draws a block/pattern/box/powerline glyph on the
// 2D context, returning whether it was handled.
func tryDrawCustomChar(ctx js.Value, c string, xOffset, yOffset, cellW, cellH float64, fontSize float64, dpr float64) bool {
	if def, ok := blockElementDefinitions[c]; ok {
		for _, box := range def {
			xEighth := cellW / 8
			yEighth := cellH / 8
			ctx.Call("fillRect",
				xOffset+float64(box.x)*xEighth, yOffset+float64(box.y)*yEighth,
				float64(box.w)*xEighth, float64(box.h)*yEighth)
		}
		return true
	}
	if def, ok := patternCharacterDefinitions[c]; ok {
		drawPatternChar(ctx, def, xOffset, yOffset, cellW, cellH)
		return true
	}
	if def, ok := boxDrawingDefinitions[c]; ok {
		drawBoxDrawingChar(ctx, def, xOffset, yOffset, cellW, cellH, dpr)
		return true
	}
	if def, ok := powerlineDefinitions[c]; ok {
		drawPowerlineChar(ctx, def, xOffset, yOffset, cellW, cellH, fontSize, dpr)
		return true
	}
	return false
}

func drawPatternChar(ctx js.Value, def [][]int, xOffset, yOffset, cellW, cellH float64) {
	// build a small pattern canvas colored with the current fillStyle
	fillStyle := ctx.Get("fillStyle").String()
	width := len(def[0])
	height := len(def)
	tmp := document.Call("createElement", "canvas")
	tmp.Set("width", width)
	tmp.Set("height", height)
	tmpCtx := tmp.Call("getContext", "2d")

	var r, g, b int
	a := 1.0
	if strings.HasPrefix(fillStyle, "#") && len(fillStyle) >= 7 {
		if _, err := fmt.Sscanf(fillStyle[1:7], "%02x%02x%02x", &r, &g, &b); err != nil {
			r, g, b = 0, 0, 0 // unparsable color: fall back to black
		}
	}
	imageData := tmpCtx.Call("createImageData", width, height)
	data := imageData.Get("data")
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			i := (y*width + x) * 4
			data.SetIndex(i, r)
			data.SetIndex(i+1, g)
			data.SetIndex(i+2, b)
			data.SetIndex(i+3, def[y][x]*int(a*255))
		}
	}
	tmpCtx.Call("putImageData", imageData, 0, 0)
	pattern := ctx.Call("createPattern", tmp, js.Null())
	ctx.Set("fillStyle", pattern)
	ctx.Call("fillRect", xOffset, yOffset, cellW, cellH)
}

func drawBoxDrawingChar(ctx js.Value, entries []boxEntry, xOffset, yOffset, cellW, cellH, dpr float64) {
	ctx.Set("strokeStyle", ctx.Get("fillStyle"))
	for _, entry := range entries {
		ctx.Call("beginPath")
		ctx.Set("lineWidth", dpr*float64(entry.weight))
		path := entry.path
		if entry.fn != nil {
			xp := .15
			yp := .15 / cellH * cellW
			path = entry.fn(xp, yp)
		}
		runSVGPath(ctx, path, cellW, cellH, xOffset, yOffset, true, dpr, 0, 0)
		ctx.Call("stroke")
		ctx.Call("closePath")
	}
}

func drawPowerlineChar(ctx js.Value, shape vectorShape, xOffset, yOffset, cellW, cellH, fontSize, dpr float64) {
	// clip the cell so drawing doesn't escape the bounds
	clip := js.Global().Get("Path2D").New()
	clip.Call("rect", xOffset, yOffset, cellW, cellH)
	ctx.Call("clip", clip)

	ctx.Call("beginPath")
	cssLineWidth := fontSize / 12
	ctx.Set("lineWidth", dpr*cssLineWidth)
	runSVGPath(ctx, shape.d, cellW, cellH, xOffset, yOffset, false, dpr,
		shape.leftPadding*(cssLineWidth/2), shape.rightPadding*(cssLineWidth/2))
	if shape.typ == vectorStroke {
		ctx.Set("strokeStyle", ctx.Get("fillStyle"))
		ctx.Call("stroke")
	} else {
		ctx.Call("fill")
	}
	ctx.Call("closePath")
}

// runSVGPath interprets the M/L/C path subset against the 2D context.
func runSVGPath(ctx js.Value, path string, cellW, cellH, xOffset, yOffset float64, doClamp bool, dpr, leftPadding, rightPadding float64) {
	for _, instruction := range strings.Split(path, " ") {
		if instruction == "" {
			continue
		}
		typ := instruction[0]
		var args []float64
		for _, s := range strings.Split(instruction[1:], ",") {
			v, err := strconv.ParseFloat(s, 64)
			if err != nil {
				v = 0 // malformed path component: keep the historical zero
			}
			args = append(args, v)
		}
		if len(args) < 2 {
			continue
		}
		// translate 0-1 coords to cell pixels with clamping and offset
		for x := 0; x < len(args); x += 2 {
			args[x] *= cellW - leftPadding*dpr - rightPadding*dpr
			if doClamp && args[x] != 0 {
				args[x] = clampF(jsRound(args[x]+0.5)-0.5, cellW, 0)
			}
			args[x] += xOffset + leftPadding*dpr
		}
		for y := 1; y < len(args); y += 2 {
			args[y] *= cellH
			if doClamp && args[y] != 0 {
				args[y] = clampF(jsRound(args[y]+0.5)-0.5, cellH, 0)
			}
			args[y] += yOffset
		}
		switch typ {
		case 'M':
			ctx.Call("moveTo", args[0], args[1])
		case 'L':
			ctx.Call("lineTo", args[0], args[1])
		case 'C':
			if len(args) >= 6 {
				ctx.Call("bezierCurveTo", args[0], args[1], args[2], args[3], args[4], args[5])
			}
		}
	}
}

func clampF(value, max, min float64) float64 {
	if value > max {
		return max
	}
	if value < min {
		return min
	}
	return value
}

// jsRound mirrors Math.round (round half up, not Go's half-away).
func jsRound(v float64) float64 {
	fl := float64(int(v))
	if v < 0 && v != fl {
		fl--
	}
	if v-fl >= 0.5 {
		return fl + 1
	}
	return fl
}
