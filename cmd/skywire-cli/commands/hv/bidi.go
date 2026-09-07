// Package clihv cmd/skywire-cli/commands/hv/bidi.go c4-vis-cli
// WebDriver BiDi support for the `hv` browser rigs, so `hv eval` and `hv probe`
// drive Waterfox/Firefox as well as Chromium/Brave.
//
// Waterfox 6.6 (Firefox ESR 128+) dropped the CDP Remote Agent, so
// --remote-debugging-port there speaks BiDi and nothing else. Until now the only
// way to drive it was a clone out of tree — exactly the failure mode `hv shell`
// warns about, and it matters precisely when it is needed: a Brave tab wedged
// badly enough that CDP never answered `1+1` left the second browser as the only
// way to take a measurement.
//
// Which protocol a port speaks is not a guess. GET /json/version answers on
// Chromium and 404s on Firefox, where /session answers 400 instead, so the
// detection below probes once and says which it picked.
//
// # The single-session rule
//
// Firefox permits ONE active BiDi session and does not release it when the
// owning socket drops. That makes the naive one-shot — dial, evaluate, exit —
// a trap: interrupt it once and the session is stranded until the browser
// restarts, which on a browser holding hours of state is an expensive mistake
// to make from a CLI. Two paths are offered instead, and both are honest about
// the cost:
//
//   - `hv drive` holds one long-lived session and tab and exposes them over a
//     local control port; `hv eval --driver` and `hv probe --driver` then spend
//     no session at all. This is the right answer when a browser is going to be
//     driven more than once, which is every real session.
//   - Without --driver, the one-shot path releases the session on every exit
//     path — a deferred End, plus a signal handler for SIGINT/SIGTERM — and
//     waits out a lagging prior teardown rather than failing on the first
//     "Maximum number of active sessions".
//
// The driver, its recovery layer and the control port are github.com/0magnet/
// wfdrive/bidi, imported rather than copied. It adds no transitive dependency:
// its only requirement is github.com/coder/websocket, the same client the CDP
// commands here already use.
package clihv

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/0magnet/wfdrive/bidi"
	"github.com/spf13/cobra"
)

// Protocols a debug port can speak.
const (
	protoCDP  = "cdp"
	protoBiDi = "bidi"
	protoAuto = "auto"
)

// detectProto asks the debug port which protocol it speaks. Chromium answers
// /json/version; Firefox 404s it and answers /session with 400 (a GET where it
// wants a websocket upgrade). Anything that answers neither is not a debug port.
func detectProto(port string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	get := func(path string) (int, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://127.0.0.1:"+port+path, nil)
		if err != nil {
			return 0, err
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return 0, err
		}
		defer resp.Body.Close()               //nolint:errcheck
		_, _ = io.Copy(io.Discard, resp.Body) //nolint:errcheck // drained only so the connection can be reused
		return resp.StatusCode, nil
	}
	code, err := get("/json/version")
	if err != nil {
		return "", fmt.Errorf("no debug port on %s: %w", port, err)
	}
	if code == http.StatusOK {
		return protoCDP, nil
	}
	if _, err := get("/session"); err != nil {
		return "", fmt.Errorf("port %s answers neither /json/version nor /session: %w", port, err)
	}
	return protoBiDi, nil
}

// resolveProto turns the --browser flag into a protocol, detecting when asked
// to and reporting what it chose. Saying which one it picked is the point: an
// automatic choice that goes unannounced is indistinguishable from a wrong one
// when the command then fails.
func resolveProto(flag, port string) (string, error) {
	switch flag {
	case protoCDP, protoBiDi:
		return flag, nil
	case protoAuto, "":
		p, err := detectProto(port)
		if err != nil {
			return "", err
		}
		fmt.Fprintf(os.Stderr, "port %s speaks %s\n", port, strings.ToUpper(p))
		return p, nil
	}
	return "", fmt.Errorf("--browser must be auto, cdp or bidi, not %q", flag)
}

// sessionWait is how long bidi.NewSession spends waiting out a prior session's
// teardown before giving up (15 tries, 2s apart).
const sessionWait = 30 * time.Second

// Which tab a one-shot BiDi session should work in.
const (
	// tabCurrent attaches to a tab that is already open — what an inspection
	// wants, since a fresh tab holds none of the state being inspected.
	tabCurrent = false
	// tabFresh opens a tab of our own — what watching a load wants, since a
	// reused tab carries whatever ran in it before.
	tabFresh = true
)

// withBiDi runs fn against a one-shot BiDi session, releasing the session on
// every exit path. Skipping the release is not a leak that clears itself: the
// next client waits for a browser restart. A tab this opened is closed again;
// one it attached to is left exactly as it was found.
func withBiDi(port string, fresh bool, timeout time.Duration, fn func(*bidi.Driver) error) error {
	// The driver's own clock has to outlast the caller's: establishing the
	// session can spend half a minute waiting out a prior teardown, and a
	// deadline that expires inside that wait reports a timeout where the real
	// answer — the session is held, here is what to do about it — was about to
	// arrive.
	ctx, cancel := context.WithTimeout(context.Background(), timeout+sessionWait+10*time.Second)
	defer cancel()
	connect := bidi.Attach
	if fresh {
		connect = bidi.Connect
	}
	d, err := connect(ctx, port)
	if err != nil {
		if strings.Contains(err.Error(), "session busy") {
			return fmt.Errorf("%w\nSomething else holds the one session Firefox allows. "+
				"Run `skywire cli hv drive` once and point --driver at it, or quit the client that holds it", err)
		}
		return err
	}
	// End last, so the tab is closed while the session that owns it still
	// exists — deferred calls run in reverse.
	defer d.End()
	if fresh {
		defer func() { _ = d.CloseTab() }() //nolint:errcheck
	} else if err := attachTab(d, bidiTab); err != nil {
		return err
	}

	// A one-shot that is Ctrl-C'd mid-command would otherwise strand the
	// session; the deferred End never runs on a signal.
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sig)
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-sig:
			d.End()
			os.Exit(130)
		case <-done:
		}
	}()
	return fn(d)
}

// bidiTab is the --tab selector: a substring of the URL of the already-open tab
// to attach to. Empty takes the first, which is right often enough to be the
// default and wrong often enough that the chosen URL is always printed.
var bidiTab string

// attachTab points the driver at a tab that is already open and says which one,
// because "the first tab" is the default selector and a browser with several
// open would otherwise answer about the wrong one without either side noticing.
func attachTab(d *bidi.Driver, want string) error {
	tabURL, err := d.AttachTab(want)
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "attached to %s\n", tabURL)
	return nil
}

// driverGet calls one endpoint on a running `hv drive` control port and decodes
// its JSON answer.
func driverGet(addr, path string, q url.Values, timeout time.Duration) (map[string]interface{}, error) {
	if !strings.Contains(addr, "://") {
		addr = "http://" + addr
	}
	u := strings.TrimSuffix(addr, "/") + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("driver at %s: %w", addr, err)
	}
	defer resp.Body.Close() //nolint:errcheck
	var out map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("driver at %s: %w", addr, err)
	}
	return out, nil
}

// driverString reads one string field out of a driver answer, treating the
// driver's own "ERROR: ..." convention as an error.
func driverString(body map[string]interface{}, field string) (string, error) {
	s, _ := body[field].(string)
	if rest, cut := strings.CutPrefix(s, "ERROR: "); cut {
		return "", fmt.Errorf("%s", rest)
	}
	return s, nil
}

var (
	drivePort string
	driveAddr string
	driveTab  string
)

func init() {
	driveCmd.Flags().StringVar(&drivePort, "port", "9223", "BiDi debug port of a running Waterfox/Firefox")
	driveCmd.Flags().StringVar(&driveAddr, "addr", "127.0.0.1:9224", "address to serve the control port on")
	driveCmd.Flags().StringVar(&driveTab, "tab", "", "substring of the URL of an already-open tab to re-attach to; a fresh tab is opened when empty")
	RootCmd.AddCommand(driveCmd)
}

var driveCmd = &cobra.Command{
	Use:   "drive",
	Short: "Hold a persistent WebDriver BiDi session and serve it over a control port",
	Long: `Hold one long-lived WebDriver BiDi session and tab against a running
Waterfox/Firefox, and expose them over a small local HTTP control port.

Firefox allows ONE active BiDi session and does not release it when the owning
socket drops, so a session per command is a hazard rather than a convenience.
Run this once and point --driver at it: hv eval and hv probe then cost no
session, and a tab closed by hand is repaired in place instead of costing a
browser restart.

  skywire cli hv drive --port 9223 --addr 127.0.0.1:9224 &
  skywire cli hv eval --driver 127.0.0.1:9224 'document.title'

Endpoints: /nav /eval /shoot /health /quit. Screenshots and console dumps land
in the temp directory under names the driver picks; each answer says where.

Needs a browser started with --remote-debugging-port=9223 --remote-allow-origins=*`,
	Run: func(_ *cobra.Command, _ []string) {
		err := bidi.Serve(context.Background(), drivePort, driveAddr, driveTab, func(tab string) {
			fmt.Printf("driving BiDi :%s (tab %s), control on http://%s\n", drivePort, tab, driveAddr)
		})
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	},
}

// probeBiDi is `hv probe` against Waterfox/Firefox. It streams the same shape
// as the CDP path — timestamps, console, exceptions, load progress — from the
// events BiDi does emit. There is no renderer-crash event to report, so a dead
// content process shows up as the stream going quiet rather than as a fault,
// which is worth knowing before reading a clean run as a healthy one.
func probeBiDi() error {
	start := time.Now()
	at := func() string { return fmt.Sprintf("[%6dms]", time.Since(start).Milliseconds()) }
	var mu sync.Mutex
	var faults []string

	return withBiDi(probePort, tabFresh, time.Duration(probeSeconds+30)*time.Second, func(d *bidi.Driver) error {
		d.OnEvent(func(m bidi.Msg) {
			if f := printBiDiEvent(at(), m, d.Tab()); f != "" {
				mu.Lock()
				faults = append(faults, f)
				mu.Unlock()
			}
		})
		if err := d.Subscribe("browsingContext.load", "browsingContext.domContentLoaded"); err != nil {
			fmt.Printf("%s could not subscribe to load events: %v\n", at(), err)
		}
		if probeNavigate != "" {
			if err := d.Navigate(probeNavigate); err != nil {
				fmt.Printf("%s navigate: %v\n", at(), err)
			}
		}
		var result string
		var gotResult bool
		if probeEval != "" {
			out, err := d.Eval(probeEval)
			if err != nil {
				fmt.Printf("%s eval: %v\n", at(), err)
			} else {
				result, gotResult = out, true
				fmt.Printf("%s result %s\n", at(), result)
			}
		}
		// A fault gate has to outlive the result: an exception thrown from a
		// timer or a promise lands after the expression that scheduled it has
		// returned, so with --fail-on-fault the watch runs its full --seconds
		// and that window is the observation period.
		if probeFailOnFault {
			if rest := time.Until(start.Add(time.Duration(probeSeconds) * time.Second)); rest > 0 {
				time.Sleep(rest)
			}
		}
		mu.Lock()
		seen := append([]string(nil), faults...)
		mu.Unlock()
		return probeVerdict(seen, result, gotResult)
	})
}

// printBiDiEvent renders one BiDi event from the tab named by want, and names it
// as a fault if it is one. Only an uncaught script error counts: pages log
// errors they have already handled, so gating on console noise would make the
// check useless — the same line the CDP path draws.
//
// The context filter is not cosmetic. CDP attaches to one target and hears only
// that target; a BiDi subscription is session-wide, so every other tab in the
// browser reports into the same stream. Left unfiltered, a --fail-on-fault run
// fails on an exception thrown in a tab the caller never asked about, which was
// observed the first time this ran: a WebTransport error from an unrelated page
// landed in the middle of a clean probe.
func printBiDiEvent(at string, m bidi.Msg, want string) string {
	clip := func(s string, n int) string {
		if len(s) > n {
			return s[:n] + "…"
		}
		return s
	}
	// An event that names no context — a worker realm, say — is kept: dropping
	// what cannot be attributed would hide the failures hardest to find.
	mine := func(got string) bool { return want == "" || got == "" || got == want }

	switch m.Method {
	case "log.entryAdded":
		var p struct {
			Type   string `json:"type"`
			Level  string `json:"level"`
			Text   string `json:"text"`
			Method string `json:"method"`
			Source struct {
				Context string `json:"context"`
			} `json:"source"`
		}
		if json.Unmarshal(m.Params, &p) != nil || !mine(p.Source.Context) {
			return ""
		}
		if p.Type == "javascript" {
			fmt.Printf("%s EXCEPTION %s\n", at, clip(p.Text, 400))
			return "uncaught exception: " + p.Text
		}
		name := p.Method
		if name == "" {
			name = p.Level
		}
		fmt.Printf("%s console.%s %s\n", at, name, clip(p.Text, 300))
	case "browsingContext.domContentLoaded", "browsingContext.load":
		var p struct {
			Context string `json:"context"`
		}
		if json.Unmarshal(m.Params, &p) != nil || !mine(p.Context) {
			return ""
		}
		if m.Method == "browsingContext.load" {
			fmt.Printf("%s load event\n", at)
		} else {
			fmt.Printf("%s DOMContentLoaded\n", at)
		}
	}
	return ""
}

// probeViaDriver probes through a running `hv drive` rather than taking the
// single BiDi session for itself. The control port answers with the console it
// collected instead of streaming it, so the timing detail the direct path gives
// is lost — and with it the ability to tell an uncaught exception from a logged
// one, which is why --fail-on-fault is refused here rather than answered
// dishonestly.
func probeViaDriver() error {
	if probeFailOnFault {
		return fmt.Errorf("--fail-on-fault needs the event stream; drop --driver, or gate on --expect")
	}
	timeout := time.Duration(probeSeconds+30) * time.Second
	q := url.Values{"wait": {fmt.Sprint(probeSeconds)}}
	if probeNavigate != "" {
		q.Set("url", probeNavigate)
	}
	if probeEval != "" {
		q.Set("eval", probeEval)
	}
	body, err := driverGet(probeDriver, "/nav", q, timeout)
	if err != nil {
		return err
	}
	if warn, ok := body["warning"].(string); ok && warn != "" {
		fmt.Printf("navigate: %s\n", warn)
	}
	if con, _ := body["console"].(string); con != "" {
		fmt.Println(con)
	}
	if png, _ := body["png"].(string); png != "" {
		fmt.Printf("screenshot %s\n", png)
	}
	result, err := driverString(body, "eval")
	if err != nil {
		return err
	}
	gotResult := probeEval != "" && result != ""
	if gotResult {
		fmt.Printf("result %s\n", result)
	}
	return probeVerdict(nil, result, gotResult)
}
