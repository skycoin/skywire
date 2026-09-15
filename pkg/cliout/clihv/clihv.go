// Package clihv is the output shape of the `skywire cli hv` commands that
// return a value. Most of them stream — a transcript, a page's console, CDP
// events as they arrive — and have no single result to shape; those are
// listed as streaming in the JSON-contract test instead.
package clihv

import (
	"fmt"
	"io"
)

// Input is what one `hv input` gesture did, and whether the page saw only
// that. RealPointerMoves counts trusted pointer moves the page received
// during the gesture that this command did not send: a hand on the mouse.
// When it is not zero the page saw a drag no test asked for, and the outcome
// is not that of the scripted input alone; FirstRealMove says where it went.
type Input struct {
	Action string `json:"action"` // click, drag or move

	X float64 `json:"x"`
	Y float64 `json:"y"`
	// Drag only: X,Y is where the press landed and ToX,ToY where it was
	// released, after Steps intermediate moves along the straight line.
	ToX   float64 `json:"to_x,omitempty"`
	ToY   float64 `json:"to_y,omitempty"`
	Steps int     `json:"steps,omitempty"`

	RealPointerMoves int         `json:"real_pointer_moves"`
	FirstRealMove    *[2]float64 `json:"first_real_move,omitempty"`
}

// Human writes the one line the command printed, and a second one when the
// real pointer interfered.
func (in Input) Human(w io.Writer) error {
	var err error
	switch in.Action {
	case "drag":
		_, err = fmt.Fprintf(w, "dragged %g,%g -> %g,%g in %d steps\n", in.X, in.Y, in.ToX, in.ToY, in.Steps)
	case "move":
		_, err = fmt.Fprintf(w, "moved to %g,%g\n", in.X, in.Y)
	default:
		_, err = fmt.Fprintf(w, "clicked %g,%g\n", in.X, in.Y)
	}
	if err != nil || in.RealPointerMoves == 0 {
		return err
	}
	_, err = fmt.Fprintf(w, "real pointer moved %d time(s) during the gesture (first to %g,%g); the page saw a drag this command did not send\n",
		in.RealPointerMoves, in.FirstRealMove[0], in.FirstRealMove[1])
	return err
}
