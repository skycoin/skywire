//go:build !mobile

// Package visor pkg/visor/skymail_outbox.go c3-vis-core
package visor

// skymailOutbox turns on the mailbox Outbox on the desktop and wasm
// visors. The phone apps keep sends immediate until they can show it.
const skymailOutbox = true
