package vt

import "strings"

// OSC 22: the mouse pointer's shape over the terminal, as xterm and kitty
// take it. A program that draws its own interface — a link it can follow, a
// divider that can be dragged — says what the pointer should look like there.
//
//	OSC 22 ; name ST        set the shape (replacing the top of the stack)
//	OSC 22 ; =name ST       the same
//	OSC 22 ; >a,b ST        push shapes (kitty)
//	OSC 22 ; < ST           pop one (kitty)
//	OSC 22 ; ?a,b ST        ask which are supported (kitty); the answer is
//	                        OSC 22 ; 1,0 ST, one 1 or 0 for each
//	OSC 22 ; ST             back to the default
//
// Names are CSS cursor names, which is what kitty uses, and xterm's X11 cursor
// font names, which are mapped to the CSS cursor that looks like them. As in
// kitty, each screen has its own stack, so a full-screen program on the
// alternate screen leaves the shell's pointer as it was.

// pointerStackMax is how deep the stack goes; kitty's limit.
const pointerStackMax = 16

// The pointer shapes a query names by role rather than by look, and what
// they are here: the I-beam the terminal shows over text, and the arrow it
// shows when a program has taken the mouse.
const (
	pointerDefaultName = "text"
	pointerGrabbedName = "default"
)

// cssPointers are the CSS cursor names a program may ask for.
var cssPointers = map[string]bool{
	"default": true, "none": true, "context-menu": true, "help": true,
	"pointer": true, "progress": true, "wait": true, "cell": true,
	"crosshair": true, "text": true, "vertical-text": true, "alias": true,
	"copy": true, "move": true, "no-drop": true, "not-allowed": true,
	"grab": true, "grabbing": true, "all-scroll": true, "col-resize": true,
	"row-resize": true, "n-resize": true, "e-resize": true, "s-resize": true,
	"w-resize": true, "ne-resize": true, "nw-resize": true, "se-resize": true,
	"sw-resize": true, "ew-resize": true, "ns-resize": true,
	"nesw-resize": true, "nwse-resize": true, "zoom-in": true, "zoom-out": true,
}

// x11Pointers maps xterm's X11 cursor font names to the CSS cursor that
// looks most like each.
var x11Pointers = map[string]string{
	"xterm":               "text",
	"ibeam":               "text",
	"left_ptr":            "default",
	"arrow":               "default",
	"top_left_arrow":      "default",
	"hand":                "pointer",
	"hand1":               "pointer",
	"hand2":               "pointer",
	"pointing_hand":       "pointer",
	"watch":               "wait",
	"clock":               "wait",
	"left_ptr_watch":      "progress",
	"question_arrow":      "help",
	"cross":               "crosshair",
	"tcross":              "crosshair",
	"crosshair":           "crosshair",
	"plus":                "cell",
	"fleur":               "move",
	"X_cursor":            "not-allowed",
	"pirate":              "not-allowed",
	"circle":              "not-allowed",
	"sb_h_double_arrow":   "ew-resize",
	"sb_v_double_arrow":   "ns-resize",
	"top_side":            "n-resize",
	"bottom_side":         "s-resize",
	"left_side":           "w-resize",
	"right_side":          "e-resize",
	"top_left_corner":     "nw-resize",
	"top_right_corner":    "ne-resize",
	"bottom_left_corner":  "sw-resize",
	"bottom_right_corner": "se-resize",
	"dotbox":              "cell",
	"copy":                "copy",
}

// PointerCSS is the CSS cursor for a pointer shape name, and whether the name
// is one this terminal knows.
func PointerCSS(name string) (string, bool) {
	if cssPointers[name] {
		return name, true
	}
	css, ok := x11Pointers[name]
	return css, ok
}

// pointerStack is the active screen's stack.
func (h *InputHandler) pointerStack() *[]string {
	if h.activeBuffer == h.bufferService.Buffers.Alt() {
		return &h.pointerAlt
	}
	return &h.pointerMain
}

// PointerShape is the CSS cursor programs have asked for on the active
// screen, or "" for the terminal's own.
func (h *InputHandler) PointerShape() string {
	s := *h.pointerStack()
	if len(s) == 0 {
		return ""
	}
	return s[len(s)-1]
}

// pointerChanged tells the browser layer the shape may have changed.
func (h *InputHandler) pointerChanged() {
	if h.OnPointerShape != nil {
		h.OnPointerShape(h.PointerShape())
	}
}

// SetPointerShape handles OSC 22.
func (h *InputHandler) SetPointerShape(data string) bool {
	stack := h.pointerStack()
	op := byte('=')
	if data != "" && strings.ContainsRune("=><?", rune(data[0])) {
		op, data = data[0], data[1:]
	}
	var names []string
	if data != "" {
		names = strings.Split(data, ",")
	}
	switch op {
	case '?':
		answers := make([]string, len(names))
		for i, n := range names {
			switch n {
			case "__current__":
				answers[i] = h.PointerShape()
				if answers[i] == "" {
					answers[i] = pointerDefaultName
				}
			case "__default__":
				answers[i] = pointerDefaultName
			case "__grabbed__":
				answers[i] = pointerGrabbedName
			default:
				answers[i] = "0"
				if _, ok := PointerCSS(n); ok {
					answers[i] = "1"
				}
			}
		}
		h.coreService.TriggerDataEvent(c0ESC+"]22;"+strings.Join(answers, ",")+c0ESC+"\\", false)
		return true
	case '<':
		if len(*stack) > 0 {
			*stack = (*stack)[:len(*stack)-1]
		}
	case '>':
		for _, n := range names {
			if css, ok := PointerCSS(n); ok {
				if len(*stack) == pointerStackMax {
					*stack = append((*stack)[:0], (*stack)[1:]...)
				}
				*stack = append(*stack, css)
			}
		}
	default: // set
		if len(names) == 0 {
			*stack = (*stack)[:0]
			break
		}
		css, ok := PointerCSS(names[0])
		if !ok {
			return true
		}
		if len(*stack) == 0 {
			*stack = append(*stack, css)
		} else {
			(*stack)[len(*stack)-1] = css
		}
	}
	h.pointerChanged()
	return true
}
