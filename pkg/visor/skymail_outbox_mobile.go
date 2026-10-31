//go:build mobile

// Package visor pkg/visor/skymail_outbox_mobile.go c3-vis-core
package visor

// skymailOutbox is off in the phone apps, which have no Outbox view
// yet: a send to an offline visor fails at once, as it always has.
const skymailOutbox = false
