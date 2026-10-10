// Package ping cmd/skywire-cli/commands/visor/ping/streamview.go c4-vis-cli
//
// The screen shared by `ping tree` and `mux-bw-tui`: fixed ANSI lines on
// top, a scrolling body fed by a gRPC stream, and a key hint. It is drawn
// with progkit, so in websh the body is also selectable HTML text.
package ping

import (
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	"github.com/0magnet/progkit"
	"github.com/gdamore/tcell/v3"
	"github.com/gdamore/tcell/v3/color"
)

var hintStyle = tcell.StyleDefault.Foreground(color.PaletteColor(241))

// sgr returns a painter for one 256-color foreground, optionally bold.
func sgr(c int, bold bool) func(string) string {
	pre := fmt.Sprintf("\x1b[38;5;%dm", c)
	if bold {
		pre = fmt.Sprintf("\x1b[1;38;5;%dm", c)
	}
	return func(s string) string { return pre + s + "\x1b[0m" }
}

// streamView is what a stream screen draws.
type streamView struct {
	// top renders the fixed lines above the body on every frame.
	top func(spin string) string
	// body renders the scrolling content after each change.
	body func() string
	// hint is the key help under the body.
	hint func() string
	// feed starts consuming the stream and calls changed after each update.
	feed func(changed func())
}

// runStreamView blocks drawing v until q, Esc or Ctrl+C.
func runStreamView(v streamView) error {
	app, err := progkit.Open()
	if err != nil {
		return err
	}
	defer app.Close()

	var dirty atomic.Bool
	dirty.Store(true)
	v.feed(func() { dirty.Store(true); app.Redraw() })

	stop := make(chan struct{})
	defer close(stop)
	go func() {
		t := time.NewTicker(150 * time.Millisecond)
		defer t.Stop()
		for {
			select {
			case <-stop:
				return
			case <-t.C:
				app.Redraw()
			}
		}
	}()

	var spin progkit.Spinner
	view := &progkit.Text{ID: "body", Follow: true, Selectable: true}
	app.Run(func(f *progkit.Frame) {
		spin.Tick()
		if dirty.Swap(false) {
			view.SetANSI(v.body())
		}
		top := progkit.ParseANSI(strings.TrimRight(v.top(spin.Frame()), "\n"), tcell.StyleDefault)
		head, rest := f.Size().SplitTop(len(top))
		for i, l := range top {
			progkit.DrawLine(f.Screen, 0, head.Y+i, head.W, l)
		}
		body, foot := rest.SplitBottom(1)
		view.Draw(f, body)
		hint := v.hint()
		if view.Follow {
			hint += " [auto]"
		}
		progkit.DrawText(f.Screen, 0, foot.Y, foot.W, hint, hintStyle)
	}, func(ev tcell.Event) bool {
		switch ev := ev.(type) {
		case *tcell.EventKey:
			switch {
			case ev.Key() == tcell.KeyEscape, progkit.IsCtrl(ev, 'c'), progkit.Typed(ev) == "q":
				return false
			case progkit.Typed(ev) == "a":
				view.Follow = !view.Follow
				if view.Follow {
					view.ScrollTo(len(view.Lines()))
				}
			case progkit.Typed(ev) == "k":
				view.ScrollTo(view.Top() - 1)
			case progkit.Typed(ev) == "j":
				view.ScrollTo(view.Top() + 1)
			case progkit.Typed(ev) == "g":
				view.ScrollTo(0)
			case progkit.Typed(ev) == "G":
				view.ScrollTo(len(view.Lines()))
			default:
				view.Key(ev)
			}
		case *tcell.EventMouse:
			view.Mouse(ev)
		}
		return true
	})
	return nil
}
