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
	"encoding/json"
	"runtime"
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
	b, err := json.Marshal(out)
	if err != nil {
		return "{}"
	}
	return string(b)
}
