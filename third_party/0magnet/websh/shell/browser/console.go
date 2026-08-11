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

var (
	logMu       sync.Mutex
	logBuf      []logEntry
	logSubs     []chan logEntry
	installOnce sync.Once
	logFuncs    []js.Func // kept alive for the page's lifetime
)

func appendLog(e logEntry) {
	logMu.Lock()
	logBuf = append(logBuf, e)
	if len(logBuf) > logBufferMax {
		logBuf = logBuf[len(logBuf)-logBufferMax:]
	}
	subs := append([]chan logEntry(nil), logSubs...)
	logMu.Unlock()
	for _, ch := range subs {
		select {
		case ch <- e:
		default: // a slow follower never blocks the console
		}
	}
}

func subscribeLogs() (<-chan logEntry, func()) {
	ch := make(chan logEntry, 256)
	logMu.Lock()
	logSubs = append(logSubs, ch)
	logMu.Unlock()
	return ch, func() {
		logMu.Lock()
		for i, c := range logSubs {
			if c == ch {
				logSubs = append(logSubs[:i], logSubs[i+1:]...)
				break
			}
		}
		logMu.Unlock()
	}
}

// joinArgs renders console.log's variadic arguments the way devtools would.
func joinArgs(args []js.Value) string {
	parts := make([]string, 0, len(args))
	for _, a := range args {
		if a.Type() == js.TypeString {
			parts = append(parts, a.String())
			continue
		}
		parts = append(parts, format(a))
	}
	return strings.Join(parts, " ")
}

// installConsoleCapture wraps console.* and the window error events once.
func installConsoleCapture() {
	installOnce.Do(func() {
		console := js.Global().Get("console")
		if !console.Truthy() {
			return
		}

		// Backfill from a host buffer that was capturing before us (skywire's
		// browse.js exposes window.skywireLog with .all()).
		if hostLog := js.Global().Get("skywireLog"); hostLog.Truthy() && hostLog.Get("all").Type() == js.TypeFunction {
			if all := hostLog.Call("all"); all.Truthy() && all.Length() > 0 {
				for i := 0; i < all.Length(); i++ {
					e := all.Index(i)
					at := time.Now()
					if t := e.Get("t"); t.Type() == js.TypeNumber {
						at = time.UnixMilli(int64(t.Float()))
					}
					appendLog(logEntry{
						at:    at,
						level: e.Get("level").String(),
						text:  e.Get("text").String(),
					})
				}
			}
		}

		for _, level := range []string{"log", "info", "warn", "error", "debug"} {
			lvl := level
			orig := console.Get(lvl)
			fn := js.FuncOf(func(_ js.Value, args []js.Value) any {
				appendLog(logEntry{at: time.Now(), level: lvl, text: joinArgs(args)})
				if orig.Type() == js.TypeFunction {
					ifaces := make([]any, len(args))
					for i, a := range args {
						ifaces[i] = a
					}
					orig.Invoke(ifaces...)
				}
				return nil
			})
			logFuncs = append(logFuncs, fn)
			console.Set(lvl, fn)
		}

		win := js.Global().Get("window")
		if win.Truthy() {
			onErr := js.FuncOf(func(_ js.Value, args []js.Value) any {
				msg := "error"
				if len(args) > 0 {
					msg = format(args[0].Get("message"))
				}
				appendLog(logEntry{at: time.Now(), level: "error", text: "[window.error] " + msg})
				return nil
			})
			onRej := js.FuncOf(func(_ js.Value, args []js.Value) any {
				msg := "unhandled rejection"
				if len(args) > 0 {
					msg = format(args[0].Get("reason"))
				}
				appendLog(logEntry{at: time.Now(), level: "error", text: "[unhandledrejection] " + msg})
				return nil
			})
			logFuncs = append(logFuncs, onErr, onRej)
			win.Call("addEventListener", "error", onErr)
			win.Call("addEventListener", "unhandledrejection", onRej)
		}
	})
}

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
		logMu.Lock()
		logBuf = nil
		logMu.Unlock()
		fmt.Fprintln(hc.Stdout, "console buffer cleared")
		return 0
	}

	keep := func(e logEntry) bool {
		return !errorsOnly || e.level == "error" || e.level == "warn"
	}

	logMu.Lock()
	snapshot := append([]logEntry(nil), logBuf...)
	logMu.Unlock()

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
