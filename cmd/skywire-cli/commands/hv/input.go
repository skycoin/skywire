// Package clihv cmd/skywire-cli/commands/hv/input.go c4-vis-cli
// `hv input` dispatches TRUSTED pointer input into a browser over CDP.
//
// This exists because `hv eval` cannot test what it looks like it can. A
// synthetic event built in the page — new MouseEvent(...) then dispatchEvent —
// invokes the listener directly, so it fires no matter where a real pointer
// is. That is fine for checking a handler runs, and useless for anything about
// how the browser ROUTES pointer input: notably, that an <iframe> is its own
// browsing context and swallows the mousemove and mouseup of a drag that
// crosses it. A synthetic drag passes whether or not that bug is present.
//
// Input.dispatchMouseEvent goes in at the browser level instead, so the events
// traverse — and are swallowed by — exactly what a hand at the mouse would.
// That makes the whole "a frame ate it" class testable without a person:
//
//	# raise a framed window by clicking the page inside it
//	skywire cli hv input ws://localhost:9222/devtools/page/ABC --click 400,300
//
//	# drag a title bar along a path that crosses another window's frame
//	skywire cli hv input ws://localhost:9222/devtools/page/ABC --drag 120,40:900,500
//
// Coordinates are viewport CSS pixels, the same space getBoundingClientRect
// reports, so a test can read a target's box with `hv eval` and aim at it.
//
// CDP only: WebDriver BiDi has input.performActions for the same job, but the
// BiDi path here addresses Firefox, where this class of bug is chased through
// `hv drive`.
package clihv

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/coder/websocket"
	"github.com/spf13/cobra"
)

var (
	inputClick   string
	inputDrag    string
	inputMove    string
	inputSteps   int
	inputPort    string
	inputTimeout int
	inputSettle  int
)

func init() {
	inputCmd.Flags().StringVar(&inputClick, "click", "", "click at X,Y (viewport CSS pixels)")
	inputCmd.Flags().StringVar(&inputDrag, "drag", "", "press at X1,Y1, move to X2,Y2, release — as X1,Y1:X2,Y2")
	inputCmd.Flags().StringVar(&inputMove, "move", "", "move the pointer to X,Y without pressing")
	inputCmd.Flags().IntVar(&inputSteps, "steps", 12, "intermediate moves in a drag; the path is what crosses a frame, so more steps is a more faithful drag")
	inputCmd.Flags().StringVar(&inputPort, "port", "9222", "debug port of the running browser, when no webSocketDebuggerUrl is given")
	inputCmd.Flags().IntVar(&inputTimeout, "timeout", 30, "seconds to wait for the browser to acknowledge")
	inputCmd.Flags().IntVar(&inputSettle, "settle-ms", 16, "pause between the events of a drag, so the page can run its handlers between them")
	RootCmd.AddCommand(inputCmd)
}

var inputCmd = &cobra.Command{
	Use:   "input [webSocketDebuggerUrl]",
	Short: "Dispatch trusted mouse input into a browser over CDP",
	Long: `Dispatch TRUSTED pointer input into a running Chromium/Brave.

Unlike a synthetic event built with "hv eval" (new MouseEvent + dispatchEvent),
these go in at the browser level, so they are routed — and swallowed by — the
same things that route and swallow a real pointer. That is what makes it
possible to test that a click inside an <iframe> raises its window, or that a
drag whose path crosses a frame still ends.

Coordinates are viewport CSS pixels, the space getBoundingClientRect reports,
so read a target's box with "hv eval" and aim at it.

  skywire cli hv input ws://localhost:9222/devtools/page/ABC --click 400,300
  skywire cli hv input ws://localhost:9222/devtools/page/ABC --drag 120,40:900,500
  skywire cli hv input --port 9222 --move 10,10`,
	Args: cobra.RangeArgs(0, 1),
	Run: func(cmd *cobra.Command, args []string) {
		if err := inputRun(cmd, args); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	},
}

// point is a viewport coordinate in CSS pixels.
type point struct{ x, y float64 }

func parsePoint(s string) (point, error) {
	parts := strings.Split(strings.TrimSpace(s), ",")
	if len(parts) != 2 {
		return point{}, fmt.Errorf("want X,Y, got %q", s)
	}
	x, err := strconv.ParseFloat(strings.TrimSpace(parts[0]), 64)
	if err != nil {
		return point{}, fmt.Errorf("x in %q: %w", s, err)
	}
	y, err := strconv.ParseFloat(strings.TrimSpace(parts[1]), 64)
	if err != nil {
		return point{}, fmt.Errorf("y in %q: %w", s, err)
	}
	return point{x, y}, nil
}

func inputRun(cmd *cobra.Command, args []string) error {
	n := 0
	for _, f := range []string{"click", "drag", "move"} {
		if cmd.Flags().Changed(f) {
			n++
		}
	}
	if n == 0 {
		return fmt.Errorf("give one of --click, --drag or --move")
	}
	if n > 1 {
		return fmt.Errorf("--click, --drag and --move are one action each; give one")
	}

	wsURL := ""
	if len(args) == 1 {
		wsURL = args[0]
	} else {
		t, err := firstPageTarget(inputPort)
		if err != nil {
			return err
		}
		wsURL = t
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(inputTimeout)*time.Second)
	defer cancel()
	c, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		return fmt.Errorf("cdp dial: %w", err)
	}
	defer c.Close(websocket.StatusNormalClosure, "") //nolint:errcheck
	c.SetReadLimit(8 << 20)

	id := 0
	// dispatch sends one event and waits for its acknowledgement. Waiting is
	// the point: the browser applies these in order, and a drag whose moves
	// are all in flight at once is not a drag the page ever sees.
	dispatch := func(params map[string]interface{}) error {
		id++
		want := id
		b, err := json.Marshal(map[string]interface{}{
			"id": want, "method": "Input.dispatchMouseEvent", "params": params,
		})
		if err != nil {
			return err
		}
		if err := c.Write(ctx, websocket.MessageText, b); err != nil {
			return fmt.Errorf("write: %w", err)
		}
		for {
			_, data, err := c.Read(ctx)
			if err != nil {
				return fmt.Errorf("read: %w", err)
			}
			var m map[string]interface{}
			if json.Unmarshal(data, &m) != nil {
				continue
			}
			if e, ok := m["error"]; ok {
				eb, _ := json.Marshal(e) //nolint:errcheck
				return fmt.Errorf("cdp: %s", string(eb))
			}
			if got, ok := m["id"].(float64); ok && int(got) == want {
				return nil
			}
		}
	}
	settle := func() { time.Sleep(time.Duration(inputSettle) * time.Millisecond) }

	move := func(p point, buttons int) error {
		return dispatch(map[string]interface{}{
			"type": "mouseMoved", "x": p.x, "y": p.y, "buttons": buttons,
		})
	}
	press := func(p point) error {
		return dispatch(map[string]interface{}{
			"type": "mousePressed", "x": p.x, "y": p.y,
			"button": "left", "buttons": 1, "clickCount": 1,
		})
	}
	release := func(p point) error {
		return dispatch(map[string]interface{}{
			"type": "mouseReleased", "x": p.x, "y": p.y,
			"button": "left", "buttons": 0, "clickCount": 1,
		})
	}

	switch {
	case cmd.Flags().Changed("move"):
		p, err := parsePoint(inputMove)
		if err != nil {
			return err
		}
		if err := move(p, 0); err != nil {
			return err
		}
		fmt.Printf("moved to %g,%g\n", p.x, p.y)

	case cmd.Flags().Changed("click"):
		p, err := parsePoint(inputClick)
		if err != nil {
			return err
		}
		// Move first: a click with no preceding move lands on a page that
		// never saw the pointer arrive, and hover-driven UI behaves
		// differently than it would under a hand.
		if err := move(p, 0); err != nil {
			return err
		}
		settle()
		if err := press(p); err != nil {
			return err
		}
		settle()
		if err := release(p); err != nil {
			return err
		}
		fmt.Printf("clicked %g,%g\n", p.x, p.y)

	case cmd.Flags().Changed("drag"):
		halves := strings.Split(inputDrag, ":")
		if len(halves) != 2 {
			return fmt.Errorf("want X1,Y1:X2,Y2, got %q", inputDrag)
		}
		from, err := parsePoint(halves[0])
		if err != nil {
			return err
		}
		to, err := parsePoint(halves[1])
		if err != nil {
			return err
		}
		if inputSteps < 1 {
			inputSteps = 1
		}
		if err := move(from, 0); err != nil {
			return err
		}
		settle()
		if err := press(from); err != nil {
			return err
		}
		settle()
		for i := 1; i <= inputSteps; i++ {
			f := float64(i) / float64(inputSteps)
			p := point{from.x + (to.x-from.x)*f, from.y + (to.y-from.y)*f}
			if err := move(p, 1); err != nil {
				return err
			}
			settle()
		}
		if err := release(to); err != nil {
			return err
		}
		fmt.Printf("dragged %g,%g -> %g,%g in %d steps\n", from.x, from.y, to.x, to.y, inputSteps)
	}
	return nil
}
