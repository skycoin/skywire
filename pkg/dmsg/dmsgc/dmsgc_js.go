//go:build js

package dmsgc

// browserRelayOnly: a browser visor served by a hypervisor rides that host's
// dmsg relay (the skynet carrier) and holds no server session while it does —
// so it has no discovery entry then (there is nothing to name in one; peers
// reach it over skynet). Off the host's LAN the relay never attaches and the
// same visor behaves as any other: server sessions over wss and a normal
// entry, so the host reaches it as its hypervisor over dmsg. #4484 stages 4
// and 7. See dmsg.Config.RelayOnly.
const browserRelayOnly = true
