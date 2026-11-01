//go:build mobile

package commands

// offline is off in the phone apps, which have no Outbox view yet: a send
// to an offline visor fails at once, and the mailbox does not offer to
// open sealed envelopes.
const offline = false
