// Package logging pkg/logging/panics.go c0-com-log
//
// Process-wide accounting for RECOVERED panics.
//
// An unrecovered panic is already captured: the visor points
// debug.SetCrashOutput at local/log/skywire-crash.log, so the full traceback
// survives the crash. A RECOVERED one has no such coverage — it is handled,
// the process keeps running, and the only trace is whatever the recovering
// site chose to log. That is a hole in two directions:
//
//   - The recovery sites disagree about what to log. Some attach
//     debug.Stack(), most log only the panic value, so even when the line is
//     found it often cannot say where the panic came from.
//   - On js/wasm there is frequently nowhere to find that line at all: no
//     stderr, the log server is off by default, the log file is not reachable
//     from the page, and both the browser console and the exec output ring
//     roll within minutes. A recovered panic there is simply invisible.
//
// A recovered panic is still a defect — a recovered panic in an RPC handler
// drops that request — so a visor panicking steadily should not report itself
// healthy on every diagnostic surface. This records a count and a small ring
// of the most recent ones so `visor state --select diag` can show them.
//
// Cost is confined to the panicking path: nothing here runs in the absence of
// a panic.
package logging

import (
	"fmt"
	"runtime/debug"
	"sync"
	"time"
)

// PanicRingSize is how many recent recovered panics are retained. Small on
// purpose: each entry carries a full stack trace, and the count — not the
// ring — is what says whether this is happening a lot.
const PanicRingSize = 8

// PanicEntry is one recovered panic.
type PanicEntry struct {
	// Time is when the panic was recovered.
	Time time.Time `json:"time"`
	// Site names the recovering location, e.g. "router RPC handler". It is
	// supplied by the caller rather than derived, because by the time a
	// deferred recover runs the panicking frames are already gone.
	Site string `json:"site"`
	// Value is the panic value, formatted with %v.
	Value string `json:"value"`
	// Stack is the traceback captured at recovery time.
	Stack string `json:"stack,omitempty"`
}

var (
	panicMu    sync.Mutex
	panicCount uint64
	panicRing  []PanicEntry
)

// RecordPanic files a recovered panic. site names the recovering location;
// v is the value recover() returned; stack is the traceback, which may be nil
// to have one captured here (callers that already hold one should pass it, as
// a stack taken at the recover point is the useful one).
//
// Safe to call from any goroutine.
func RecordPanic(site string, v interface{}, stack []byte) {
	if stack == nil {
		stack = debug.Stack()
	}
	e := PanicEntry{
		Time:  time.Now(),
		Site:  site,
		Value: fmt.Sprintf("%v", v),
		Stack: string(stack),
	}

	panicMu.Lock()
	defer panicMu.Unlock()
	panicCount++
	panicRing = append(panicRing, e)
	if len(panicRing) > PanicRingSize {
		panicRing = panicRing[len(panicRing)-PanicRingSize:]
	}
}

// PanicStats reports the total number of recovered panics since start and the
// most recent PanicRingSize of them, oldest first. The count keeps rising
// after the ring has wrapped, which is the point: it says whether what the
// ring shows is the whole story.
func PanicStats() (count uint64, last []PanicEntry) {
	panicMu.Lock()
	defer panicMu.Unlock()
	if len(panicRing) == 0 {
		return panicCount, nil
	}
	out := make([]PanicEntry, len(panicRing))
	copy(out, panicRing)
	return panicCount, out
}

// ResetPanicStats clears the count and the ring. For tests.
func ResetPanicStats() {
	panicMu.Lock()
	defer panicMu.Unlock()
	panicCount = 0
	panicRing = nil
}

// LogRecovered records and logs a panic that the caller has ALREADY
// recovered. Use it from inside a deferred function that has other cleanup to
// do as well, which is the common shape:
//
//	defer func() {
//		if rec := recover(); rec != nil {
//			logging.LogRecovered(log, "router RPC handler", rec)
//		}
//		conn.Close()
//	}()
//
// The stack is captured here rather than taken from the caller, so every site
// gets one without having to remember debug.Stack(). log may be nil, in which
// case the panic is recorded but not logged.
func LogRecovered(log *Logger, site string, v interface{}) {
	stack := debug.Stack()
	RecordPanic(site, v, stack)
	if log != nil {
		log.WithField("stack", string(stack)).Errorf("panic recovered in %s: %v", site, v)
	}
}

// Recover is the whole-defer form, for a site whose deferred function does
// nothing but recover:
//
//	defer logging.Recover(log, "dmsg read loop")
//
// It deliberately does NOT re-panic. A site that wants the panic to propagate
// should not be recovering in the first place.
func Recover(log *Logger, site string) {
	if r := recover(); r != nil {
		LogRecovered(log, site, r)
	}
}
