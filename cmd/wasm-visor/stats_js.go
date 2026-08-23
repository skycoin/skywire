//go:build js && wasm

// Package main cmd/wasm-visor/stats_js.go c3-vis-wasm
// stats_js.go — a lightweight "pprof stand-in" for the browser visor. Real
// net/http/pprof isn't reachable inside a tab, but the numbers that actually
// matter for spotting leaks and comparing the Go vs TinyGo builds (and both vs
// the native host visor) ARE cheap to read at runtime: goroutine count,
// heap/alloc/GC, plus the live subsystem cardinalities (dmsg sessions,
// transports, routes, skysocks sessions, proxy pool). visorStats() returns them
// as JSON; poll it over time (harness / hvinspect) to watch for unbounded
// growth — a goroutine or session count that climbs monotonically is the tell.
package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"runtime"
	"runtime/pprof"
	"syscall/js"
	"time"
)

// buildVariant reports which toolchain compiled this blob so a comparison run can
// label its samples. TinyGo sets runtime.Compiler to "tinygo"; std Go is "gc".
func buildVariant() string {
	if runtime.Compiler == "tinygo" {
		return "tinygo"
	}
	return "go"
}

// jsVisorStats() → JSON snapshot of runtime + subsystem counters. Safe to call
// before boot (fields simply default to zero / absent).
func jsVisorStats(js.Value, []js.Value) interface{} {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	out := map[string]interface{}{
		"variant":      buildVariant(),
		"goroutines":   runtime.NumGoroutine(),
		"heap_alloc":   m.HeapAlloc,
		"heap_inuse":   m.HeapInuse,
		"heap_sys":     m.HeapSys,
		"heap_objects": m.HeapObjects,
		"sys":          m.Sys,
		"num_gc":       m.NumGC,
	}
	if !bootTime.IsZero() {
		out["uptime_sec"] = int64(time.Since(bootTime).Seconds())
	}
	if !selfPK.Null() {
		out["pk"] = selfPK.Hex()
	}
	if dmsgC != nil {
		out["dmsg_sessions"] = len(dmsgC.AllSessions())
	}
	if tpM != nil {
		out["transports"] = tpM.TransportCount()
	}
	if rtr != nil {
		out["routes"] = rtr.RoutesCount()
	}
	skysocksMu.Lock()
	out["skysocks_sessions"] = len(skysocksSessions)
	skysocksMu.Unlock()
	proxyPoolMu.Lock()
	out["proxy_pool"] = len(proxyPool)
	proxyPoolMu.Unlock()
	// Transport-registration / TPD-sync introspection — the wasm analog of
	// `skywire cli visor state`'s registration view. Answers "why does a route-find
	// to this browser visor say transport not found": what we've published to TPD
	// (tp_list), whether the CXO publish is FROZEN on a stale snapshot
	// (publish_state), and whether our announces reach TPD (announce).
	out["transport_registration"] = transportRegistrationSnapshot()
	b, err := json.Marshal(out)
	if err != nil {
		return "{}"
	}
	return string(b)
}

// jsGoroutineDump() → the full text stack trace of EVERY goroutine
// (runtime.Stack(_, true): the SIGQUIT-style dump). The one thing visorStats()
// can't show is WHICH goroutine is hot — a goroutine wedged in a busy
// select+time.After loop shows up here by its stack, and counting identical
// stacks reveals a per-transport / per-session spinner. Uses a 2 MB scratch
// buffer (trivial next to the heap).
func jsGoroutineDump(js.Value, []js.Value) interface{} {
	buf := make([]byte, 2<<20)
	n := runtime.Stack(buf, true)
	return string(buf[:n])
}

// jsPprof(name) → a runtime/pprof profile in the binary protobuf format that
// `go tool pprof` reads, base64-encoded so the JS side can Blob-download it as
// <name>.pb.gz. SNAPSHOT profiles only (goroutine / heap / allocs /
// threadcreate / mutex / block) — those work under wasm; CPU profiling needs
// SIGPROF, which wasm has no equivalent for (use the DevTools sampler for CPU).
// mutex/block are empty unless their rate was enabled. Defaults to "goroutine".
func jsPprof(_ js.Value, args []js.Value) interface{} {
	name := "goroutine"
	if len(args) > 0 && args[0].Type() == js.TypeString && args[0].String() != "" {
		name = args[0].String()
	}
	p := pprof.Lookup(name)
	if p == nil {
		return ""
	}
	var b bytes.Buffer
	if err := p.WriteTo(&b, 0); err != nil { // 0 = protobuf (for `go tool pprof`)
		return ""
	}
	return base64.StdEncoding.EncodeToString(b.Bytes())
}
