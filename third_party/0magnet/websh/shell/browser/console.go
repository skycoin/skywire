//go:build js && wasm

package browser

// Console capture: the browser console, readable from the shell.
//
// console.log and friends are wrapped once so their output lands in a ring
// buffer as well as devtools, and window errors / unhandled rejections are
// recorded too. Wrapping chains — the previous console.* is still called — so
// this composes with a host page that already captures (skywire's browse.js
// does exactly this into window.skywireLog, and that buffer is used to
// backfill entries from before the shell existed).

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"syscall/js"
	"time"

	"github.com/skycoin/skywire/third_party/0magnet/sh/v3/interp"
	"github.com/skycoin/skywire/third_party/0magnet/websh/shell"
)

// logEntry is one captured console line.
type logEntry struct {
	at    time.Time
	level string
	text  string
}

const logBufferMax = 5000

var installOnce sync.Once

// ringHandle returns the JS-side capture ring, or a falsy value before install.
func ringHandle() js.Value { return js.Global().Get("__webshLog") }

// readLogsSince returns the entries captured after seq, and the new high-water
// mark. Reading in batches is the entire point of keeping the ring in JS: see
// the note on captureJS.
func readLogsSince(seq int64) ([]logEntry, int64) {
	r := ringHandle()
	if !r.Truthy() {
		return nil, seq
	}
	arr := r.Call("since", seq)
	if !arr.Truthy() {
		return nil, seq
	}
	n := arr.Length()
	out := make([]logEntry, 0, n)
	for i := 0; i < n; i++ {
		e := arr.Index(i)
		out = append(out, logEntry{
			at:    time.UnixMilli(int64(e.Get("at").Float())),
			level: e.Get("level").String(),
			text:  e.Get("text").String(),
		})
		seq = int64(e.Get("n").Float())
	}
	return out, seq
}

// subscribeLogs polls the ring for a follower. Polling is deliberate: the
// console is driven by whatever the page happens to be doing, which in a visor
// tab is a continuous stream, and one wasm call per line is what wedged the
// page. A follower that wakes ten times a second is imperceptible to a human
// reading a terminal and costs a bounded number of calls per second.
func subscribeLogs() (<-chan logEntry, func()) {
	ch := make(chan logEntry, 256)
	done := make(chan struct{})
	go func() {
		defer close(ch)
		_, seq := readLogsSince(0) // start from now; the backlog was already printed
		t := time.NewTicker(100 * time.Millisecond)
		defer t.Stop()
		for {
			select {
			case <-done:
				return
			case <-t.C:
				var batch []logEntry
				batch, seq = readLogsSince(seq)
				for _, e := range batch {
					select {
					case ch <- e:
					case <-done:
						return
					default: // a slow follower never stalls the poller
					}
				}
			}
		}
	}()
	return ch, func() { close(done) }
}

// installConsoleCapture installs the JS-side capture once.
func installConsoleCapture() {
	installOnce.Do(func() {
		if !js.Global().Get("console").Truthy() {
			return
		}
		if r := js.Global().Call("eval", captureJS); r.Type() == js.TypeFunction {
			r.Invoke(logBufferMax)
		}
	})
}

// captureJS installs the capture entirely in JS: console.* and the window error
// events append to a ring that Go reads in batches, on demand.
//
// Nothing here calls into wasm, and that is the whole design. The first version
// wrapped console.* around a Go sink, one wasm call per line. Two things went
// wrong with it. The first was recursion — Go's own stdout leaves through
// console.log (wasm_exec.js), so the sink was reachable from inside a
// wasm-initiated call, and anything it logged called it again until "Maximum
// call stack size exceeded" left the module corrupt; that was patched with a
// re-entrancy guard in JS.
//
// The second was fatal and is why the guard was not enough. The standard-Go
// runtime returns to the JS event loop only when every goroutine is blocked. A
// visor tab's console is not a trickle — the worker forwards transport dials,
// WebRTC signalling and dmsg debug output continuously — and each line entering
// Go gave the scheduler more work before it could park. Past some rate it never
// parks: the renderer spins at full CPU inside wasm, the page stops answering
// anything (a bare `typeof` included), the terminal window will not even drag,
// and because it never reaches a JS statement boundary the debugger cannot
// interrupt it either. Measured on a quiet page the sink cost 4.6x a bare
// console.log, which a finite burst survives and an endless stream does not.
//
// So the boundary is crossed once per `logs` invocation, or ten times a second
// while following, instead of once per line. This is the same rule the WebGL
// overlay follows: per-event work stays on the JS side of the module.
const captureJS = `(function (max) {
	var g = globalThis;
	if (g.__webshLog) { return g.__webshLog; }

	function render(a) {
		if (typeof a === 'string') { return a; }
		if (a instanceof Error) { return String(a && a.stack || a); }
		try { return JSON.stringify(a); } catch (e) { return String(a); }
	}
	var ring = { buf: [], max: max, seq: 0 };
	ring.push = function (level, text) {
		ring.seq++;
		ring.buf.push({ at: Date.now(), level: level, text: text, n: ring.seq });
		if (ring.buf.length > ring.max) { ring.buf.splice(0, ring.buf.length - ring.max); }
	};
	ring.since = function (n) {
		var out = [];
		for (var i = ring.buf.length - 1; i >= 0; i--) {
			if (ring.buf[i].n <= n) { break; }
			out.unshift(ring.buf[i]);
		}
		return out;
	};
	ring.clear = function () { ring.buf.length = 0; };
	g.__webshLog = ring;

	// Backfill from a host buffer that was capturing before us (skywire's
	// browse.js exposes window.skywireLog with .all()), so output from before
	// this shell opened is there too.
	try {
		var host = g.skywireLog;
		if (host && typeof host.all === 'function') {
			var all = host.all() || [];
			for (var j = 0; j < all.length; j++) {
				var h = all[j];
				ring.push((h && h.level) || 'log', render(h && h.text !== undefined ? h.text : h));
				// keep the host's timestamp rather than stamping the backfill now
				if (h && typeof h.t === 'number') { ring.buf[ring.buf.length - 1].at = h.t; }
			}
		}
	} catch (e) {}

	['log', 'info', 'warn', 'error', 'debug'].forEach(function (lvl) {
		var orig = console[lvl];
		if (typeof orig !== 'function') { return; }
		console[lvl] = function () {
			try {
				var parts = [];
				for (var i = 0; i < arguments.length; i++) { parts.push(render(arguments[i])); }
				ring.push(lvl, parts.join(' '));
			} catch (e) { /* never let capture break logging */ }
			return orig.apply(console, arguments);
		};
	});

	if (typeof g.addEventListener === 'function') {
		g.addEventListener('error', function (ev) {
			ring.push('error', '[window.error] ' + render(ev && ev.message));
		});
		g.addEventListener('unhandledrejection', function (ev) {
			ring.push('error', '[unhandledrejection] ' + render(ev && ev.reason));
		});
	}
	return ring;
})`

// colorFor tints a level the way the pager and ls do — errors red, warnings
// amber, debug dim.
func colorFor(level string) (string, string) {
	switch level {
	case "error":
		return "\x1b[31m", "\x1b[0m"
	case "warn":
		return "\x1b[33m", "\x1b[0m"
	case "debug":
		return "\x1b[2m", "\x1b[0m"
	default:
		return "", ""
	}
}

func renderLog(e logEntry, plain bool) string {
	stamp := e.at.Format("15:04:05")
	if plain {
		return fmt.Sprintf("%s %-5s %s", stamp, e.level, e.text)
	}
	on, off := colorFor(e.level)
	return fmt.Sprintf("\x1b[2m%s\x1b[0m %s%-5s%s %s", stamp, on, e.level, off, e.text)
}

// runLogs implements the `logs` applet: the browser console in the shell.
func runLogs(ctx context.Context, s *shell.Shell, hc *interp.HandlerContext, args []string) int {
	follow, errorsOnly, clear, plain := false, false, false, false
	limit := 0
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "-f":
			follow = true
		case "-e":
			errorsOnly = true
		case "-c":
			clear = true
		case "-p":
			plain = true
		case "-n":
			if i+1 < len(args) {
				i++
				limit, _ = strconv.Atoi(args[i])
			}
		default:
			if strings.HasPrefix(args[i], "-n") {
				limit, _ = strconv.Atoi(args[i][2:])
			}
		}
	}
	if clear {
		if r := ringHandle(); r.Truthy() {
			r.Call("clear")
		}
		fmt.Fprintln(hc.Stdout, "console buffer cleared")
		return 0
	}

	keep := func(e logEntry) bool {
		return !errorsOnly || e.level == "error" || e.level == "warn"
	}

	snapshot, _ := readLogsSince(0)

	filtered := make([]logEntry, 0, len(snapshot))
	for _, e := range snapshot {
		if keep(e) {
			filtered = append(filtered, e)
		}
	}
	if limit > 0 && limit < len(filtered) {
		filtered = filtered[len(filtered)-limit:]
	}
	for _, e := range filtered {
		fmt.Fprintln(hc.Stdout, renderLog(e, plain))
	}

	if !follow {
		if len(filtered) == 0 {
			fmt.Fprintln(hc.Stderr, "(console buffer empty — output is captured from the moment the shell starts)")
		}
		return 0
	}

	ch, unsubscribe := subscribeLogs()
	defer unsubscribe()
	for {
		select {
		case <-ctx.Done():
			return 130
		case e := <-ch:
			if keep(e) {
				fmt.Fprintln(hc.Stdout, renderLog(e, plain))
			}
		}
	}
}
