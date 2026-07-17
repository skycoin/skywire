//go:build js && wasm

// Package servicedisc pkg/servicedisc/stringarray_js.go
//
// Browser build: LocalIPs is a plain []string. The Postgres-backed
// pq.StringArray (stringarray_native.go) is only needed by the service
// registry's SQL store, which never runs in a browser tab — and lib/pq
// won't compile under the TinyGo fork anyway. JSON wire format is identical.
package servicedisc

type stringArray = []string
