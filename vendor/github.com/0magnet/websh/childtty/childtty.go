// Package childtty is a program's terminal, asked first what its host
// offers (progressive.Probe), with the host's events taken out of its input,
// as a tcell Tty:
//
//	s, err := childtty.NewScreen()
//
// is all a tcell program needs, wherever it runs. When websh runs it from the
// filesystem it is a child process under bottle's proc with a terminal of its
// own: size, raw mode and resizes come from the shell, keys are read and the
// screen drawn through the terminal itself, so it works whichever toolchain
// built the program. Natively it is /dev/tty: a desktop terminal (kitty
// answers its graphics query), websh at the far end of an ssh session (it
// answers everything), or one that answers neither (cells). Where there is no
// terminal it falls back to tcell's own screen.
//
// A program that writes sequences of its own beside tcell's (websh
// placements, say) writes them to the Tty Open returns, so they land in
// order: TinyGo holds os.Stdout back until a newline. A program TinyGo built
// must also end with os.Exit, since TinyGo keeps a js program alive after
// main returns.
package childtty

import (
	"time"

	"github.com/gdamore/tcell/v3"

	"github.com/0magnet/websh/progressive"
)

// events carries the host's events once Open has run.
var events <-chan *progressive.Event

// Events is what happens to this program's placements made with Events —
// clicks, and messages from their widgets — as the host reports it. Each is a
// tcell.Event too, so a tcell program can pass them into its own queue:
//
//	go func() {
//		for e := range childtty.Events() {
//			screen.EventQ() <- e
//		}
//	}()
//
// It is nil before Open, and where there is no terminal.
func Events() <-chan *progressive.Event { return events }

// probeWait is how long a terminal that answers nothing is waited for.
const probeWait = 2 * time.Second

// NewScreen is a tcell screen on this program's terminal, or tcell's own
// screen where it has none.
func NewScreen() (tcell.Screen, error) {
	if t, ok := Open(); ok {
		return tcell.NewTerminfoScreenFromTty(t)
	}
	return tcell.NewScreen()
}
