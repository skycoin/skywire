//go:build js

package dmsgc

// browserUnpublished: a browser visor publishes no dmsg discovery entry. It is
// reached over skynet (its host's transport, the forwarding mux) and its own
// dmsg traffic rides its relay; an entry would only advertise server sessions
// it does not hold. Route setup no longer needs the entry either — the
// source-driven cascade is the browser default (#4730). Without an entry the
// client also holds no server-session floor once a relay is attached (see
// dmsg.Client sessionsSatisfied / reapExcessIdleSessions): #4484 stage 4.
const browserUnpublished = true
