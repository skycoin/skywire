// Package clihv cmd/skywire-cli/commands/hv/probe.go c4-vis-cli
// `hv probe` watches one page load and streams what the page reports: console
// output, uncaught exceptions, renderer crashes and load progress.
//
// It exists because "the tab is not answering" is not a diagnosis. A busy main
// thread, a page with no execution context, a stalled navigation and a crashed
// renderer all look identical from outside — `hv eval` simply returns nothing
// for every one of them, and guessing which it is has cost several wrong
// diagnoses. Watching the event stream during a load tells them apart, and it
// is how the orphaned-SharedWorker bug was found: the page logged its fallback
// at 45.7s, which named the failing wait exactly.
//
//	skywire cli hv probe --navigate http://127.0.0.1:7998/ --seconds 90
//	skywire cli hv probe --ws ws://localhost:9222/devtools/page/ABC --eval 'typeof skywireVisor'
//
// With no --ws it opens a fresh tab, which is usually what you want: a reused
// tab carries the state of whatever ran in it before.
package clihv

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/coder/websocket"
	"github.com/spf13/cobra"
)

var (
	probePort     string
	probeWS       string
	probeNavigate string
	probeEval     string
	probeSeconds  int
)

func init() {
	probeCmd.Flags().StringVar(&probePort, "port", "9222", "CDP port of the running browser")
	probeCmd.Flags().StringVar(&probeWS, "ws", "", "webSocketDebuggerUrl of an existing target; a fresh tab is opened when empty")
	probeCmd.Flags().StringVar(&probeNavigate, "navigate", "", "URL to load while watching")
	probeCmd.Flags().StringVar(&probeEval, "eval", "", "expression to evaluate; the stream is printed until the result arrives")
	probeCmd.Flags().IntVar(&probeSeconds, "seconds", 60, "how long to watch")
	RootCmd.AddCommand(probeCmd)
}

var probeCmd = &cobra.Command{
	Use:   "probe",
	Short: "Watch a page load over CDP and stream console, exceptions and crashes",
	Long: `Watch one page over CDP and stream what it reports: console output,
uncaught exceptions, renderer crashes, execution contexts and load progress.

Use it when a tab stops answering. "hv eval returned nothing" cannot
distinguish a busy main thread from a missing execution context, a stalled
navigation or a dead renderer; this can.

Needs a browser started with --remote-debugging-port=9222.`,
	Run: func(_ *cobra.Command, _ []string) {
		if err := probeRun(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	},
}

func probeRun() error {
	if probeNavigate == "" && probeEval == "" {
		return fmt.Errorf("give --navigate or --eval (or both)")
	}
	wsURL := probeWS
	var opened string
	if wsURL == "" {
		tab, err := newTab(probePort)
		if err != nil {
			return err
		}
		wsURL, opened = tab.WS, tab.ID
		defer func() { _ = closeTab(probePort, opened) }() //nolint:errcheck
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(probeSeconds+15)*time.Second)
	defer cancel()
	c, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		return fmt.Errorf("cdp dial: %w", err)
	}
	defer c.Close(websocket.StatusNormalClosure, "") //nolint:errcheck
	c.SetReadLimit(64 << 20)

	id := 0
	start := time.Now()
	send := func(method string, params map[string]interface{}) int {
		id++
		if params == nil {
			params = map[string]interface{}{}
		}
		b, _ := json.Marshal(map[string]interface{}{"id": id, "method": method, "params": params}) //nolint:errcheck
		_ = c.Write(ctx, websocket.MessageText, b)                                                 //nolint:errcheck
		return id
	}
	at := func() string { return fmt.Sprintf("[%6dms]", time.Since(start).Milliseconds()) }

	send("Runtime.enable", nil)
	send("Page.enable", nil)
	send("Log.enable", nil)
	send("Inspector.enable", nil)
	if probeNavigate != "" {
		send("Page.navigate", map[string]interface{}{"url": probeNavigate})
	}
	wantID := 0
	if probeEval != "" {
		wantID = send("Runtime.evaluate", map[string]interface{}{
			"expression": probeEval, "returnByValue": true, "awaitPromise": true,
		})
	}

	deadline := time.Now().Add(time.Duration(probeSeconds) * time.Second)
	for time.Now().Before(deadline) {
		_, data, err := c.Read(ctx)
		if err != nil {
			// A watch that runs out its clock is the normal ending, not a fault.
			fmt.Printf("%s stream ended: %v\n", at(), err)
			return nil
		}
		var m probeMsg
		if json.Unmarshal(data, &m) != nil {
			continue
		}
		printProbeEvent(at(), m)
		if wantID != 0 && m.ID == wantID {
			fmt.Printf("%s result %s\n", at(), string(m.Result))
			return nil
		}
	}
	return nil
}

// probeMsg is the slice of the CDP event shape this reads.
type probeMsg struct {
	ID     int             `json:"id"`
	Method string          `json:"method"`
	Result json.RawMessage `json:"result"`
	Params struct {
		Type string `json:"type"`
		Args []struct {
			Value       interface{} `json:"value"`
			Description string      `json:"description"`
		} `json:"args"`
		ExceptionDetails struct {
			Text      string `json:"text"`
			Exception struct {
				Description string `json:"description"`
			} `json:"exception"`
		} `json:"exceptionDetails"`
		Entry struct {
			Level string `json:"level"`
			Text  string `json:"text"`
		} `json:"entry"`
		Name string `json:"name"`
	} `json:"params"`
}

func printProbeEvent(at string, m probeMsg) {
	clip := func(s string, n int) string {
		if len(s) > n {
			return s[:n] + "…"
		}
		return s
	}
	switch m.Method {
	case "Runtime.consoleAPICalled":
		parts := make([]string, 0, len(m.Params.Args))
		for _, a := range m.Params.Args {
			s := a.Description
			if a.Value != nil {
				s = fmt.Sprint(a.Value)
			}
			parts = append(parts, s)
		}
		fmt.Printf("%s console.%s %s\n", at, m.Params.Type, clip(strings.Join(parts, " "), 300))
	case "Runtime.exceptionThrown":
		d := m.Params.ExceptionDetails.Exception.Description
		if d == "" {
			d = m.Params.ExceptionDetails.Text
		}
		fmt.Printf("%s EXCEPTION %s\n", at, clip(d, 400))
	case "Log.entryAdded":
		fmt.Printf("%s log.%s %s\n", at, m.Params.Entry.Level, clip(m.Params.Entry.Text, 300))
	case "Inspector.targetCrashed":
		fmt.Printf("%s *** RENDERER CRASHED ***\n", at)
	case "Inspector.detached":
		fmt.Printf("%s *** DETACHED: %s ***\n", at, m.Params.Name)
	case "Runtime.executionContextCreated":
		fmt.Printf("%s execution context created\n", at)
	case "Page.loadEventFired":
		fmt.Printf("%s load event\n", at)
	case "Page.frameStoppedLoading":
		fmt.Printf("%s frame stopped loading\n", at)
	}
}
