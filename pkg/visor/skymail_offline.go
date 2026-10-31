//go:build !mobile

// Package visor pkg/visor/skymail_offline.go c3-vis-core
package visor

// skymailOffline turns on mail for visors that are offline on the
// desktop and wasm visors: the Outbox, and opening sealed envelopes
// other visors relay. The phone apps keep sends immediate until they
// can show it.
const skymailOffline = true
