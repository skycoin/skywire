//go:build mobile

// Package visor pkg/visor/skymail_offline_mobile.go c3-vis-core
package visor

// skymailOffline is off in the phone apps, which have no Outbox view
// yet: a send to an offline visor fails at once, as it always has, and
// the mailbox does not offer to open sealed envelopes.
const skymailOffline = false
