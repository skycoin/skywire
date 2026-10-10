// Package livetui cmd/skywire-cli/cliutil/livetui/livetui.go c4-vis-cli
// helper. Wraps any function that produces a string in a scrollable
// view that re-renders on a configurable tick, watch(1)-style, without
// rewriting the whole screen each tick.
//
// Build a Refresh callback that returns the snapshot to show now and pass
// it to Run with an interval. Run blocks until q, Esc or Ctrl+C. The view
// scrolls independently of new frames, and stays at the end when it was
// there. It is drawn with progkit, so in websh the snapshot is also laid
// over the cells as selectable text.
package livetui

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/0magnet/progkit"
	"github.com/gdamore/tcell/v3"
	"github.com/gdamore/tcell/v3/color"
)

// Refresh produces the snapshot to display on each tick. ctx is canceled
// when the user quits. Errors are shown in the header, and the loop goes
// on, so a transient error can be watched recovering.
type Refresh func(ctx context.Context) (string, error)

// Options tunes a Run invocation. All fields are optional.
type Options struct {
	// Title shown in the header. Defaults to "skywire live".
	Title string
	// Interval between Refresh calls. Defaults to 1s.
	Interval time.Duration
}

func (o Options) withDefaults() Options {
	if o.Title == "" {
		o.Title = "skywire live"
	}
	if o.Interval <= 0 {
		o.Interval = time.Second
	}
	return o
}

var (
	titleStyle = tcell.StyleDefault.Bold(true).Foreground(color.PaletteColor(205))
	errStyle   = tcell.StyleDefault.Foreground(color.PaletteColor(196))
	dimStyle   = tcell.StyleDefault.Foreground(color.PaletteColor(241))
	spinStyle  = tcell.StyleDefault.Foreground(color.PaletteColor(86))
)

// Run blocks running the view until the user quits.
func Run(refresh Refresh, opts Options) error {
	opts = opts.withDefaults()
	app, err := progkit.Open()
	if err != nil {
		return err
	}
	defer app.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var (
		mu       sync.Mutex
		pending  *string
		lastErr  string
		ticks    int
		spin     progkit.Spinner
		started  = time.Now()
		view     = &progkit.Text{ID: "live", Follow: true, Selectable: true}
		frameDue = make(chan struct{}, 1)
	)
	fetch := func() {
		out, err := refresh(ctx)
		if ctx.Err() != nil {
			return
		}
		mu.Lock()
		ticks++
		if err != nil {
			lastErr = err.Error()
		} else {
			lastErr = ""
			pending = &out
		}
		mu.Unlock()
		app.Redraw()
	}
	go func() {
		fetch()
		t := time.NewTicker(opts.Interval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				select {
				case frameDue <- struct{}{}:
					go func() { fetch(); <-frameDue }()
				default: // the last refresh is still running
				}
			}
		}
	}()
	go func() {
		t := time.NewTicker(150 * time.Millisecond)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				app.Redraw()
			}
		}
	}()

	app.Run(func(f *progkit.Frame) {
		mu.Lock()
		if pending != nil {
			view.SetANSI(*pending)
			pending = nil
		}
		errText, n := lastErr, ticks
		mu.Unlock()
		spin.Tick()

		head, body := f.Size().SplitTop(1)
		body, foot := body.SplitBottom(1)
		x := progkit.DrawText(f.Screen, 0, head.Y, head.W, spin.Frame()+" ", spinStyle)
		x += progkit.DrawText(f.Screen, x, head.Y, head.W-x, opts.Title, titleStyle)
		x += progkit.DrawText(f.Screen, x, head.Y, head.W-x, fmt.Sprintf(" | tick #%d every %s | elapsed %s", n, opts.Interval, time.Since(started).Truncate(time.Second)), tcell.StyleDefault)
		if errText != "" {
			progkit.DrawText(f.Screen, x+2, head.Y, head.W-x-2, "err: "+errText, errStyle)
		}
		view.Draw(f, body)
		progkit.DrawText(f.Screen, 0, foot.Y, foot.W, "↑/↓ scroll | g/G top/bottom | q quit", dimStyle)
	}, func(ev tcell.Event) bool {
		switch ev := ev.(type) {
		case *tcell.EventKey:
			switch {
			case ev.Key() == tcell.KeyEscape, progkit.IsCtrl(ev, 'c'), progkit.Typed(ev) == "q":
				return false
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
