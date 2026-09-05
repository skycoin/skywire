// Package dmsgclient pkg/dmsg/dmsgclient/doc.go c1-net-dmsg
//
// dmsgclient provides the dmsg client constructors used by the CLI tools, the
// wasm builds and the deployment-adjacent services. Picking the wrong one is a
// real and recurring bug — see the decision matrix below before adding a
// caller.
//
// # Two axes, not nine constructors
//
// Every constructor here is a point on two axes:
//
//   - HOW SERVERS ARE FOUND: query a dmsg-discovery, or start from a seeded
//     set of server entries (embedded in the deployment config).
//   - WHETHER WE PUBLISH: register our own entry in the discovery, so other
//     peers can dial us, or stay seed-only and remain undialable-by-lookup.
//
// The second axis is the one that bites. A seed-only client can reach the peers
// it was seeded with and nothing else, and nobody can find it. That is correct
// for a read-only consumer with a fixed set of destinations and wrong for
// anything that must be dialed.
//
//	                  | publishes our entry      | seed-only (no entry)
//	------------------+--------------------------+---------------------------
//	discovery-based   | StartDmsg                | —
//	                  | StartDmsgSelfHostedDisc  |
//	------------------+--------------------------+---------------------------
//	seeded / embedded | StartDmsgEmbedded        | StartDmsgEmbeddedForServices
//	                  | StartDmsgSeeded (with a  | StartDmsgSeeded (with an
//	                  | discovery address)       | empty discovery address)
//	------------------+--------------------------+---------------------------
//	one fixed peer    | StartDmsgDirectWithServers
//
// # Deployment services publish no discovery entry
//
// TPD, SD, AR, RF and the route setup nodes deliberately do NOT publish a
// dmsg-discovery entry — that is what saves every caller a lookup round-trip.
// A client that dials them must therefore SEED them, or each request fails with
// "dmsg error 100 - entry is not found in discovery".
//
// This is not hypothetical: the reward server used StartDmsgEmbedded with no
// seeded service PKs, and every stats panel without a CXO feed to fall back on
// rendered "unavailable" in production until the PKs were seeded. If a caller
// both dials deployment services AND needs to be dialable, it wants
// StartDmsgEmbedded WITH servicePKs — publishing and seeding are independent.
//
// # The visor does not use this package
//
// pkg/visor builds its dmsg client inline in init_dmsg.go (direct.GetAllEntries
// plus dmsgdisc.NewHTTP) rather than calling anything here, so the most
// exercised setup in the codebase is not the shared one. Converging the two is
// tracked separately; until then, do not assume a change here reaches the
// visor, or that visor behavior is reproduced by these constructors.
//
// # Unwired constructors
//
// StartDmsgWithSyntheticDiscovery and StartDmsgWithDirectClient currently have
// no non-test callers. They are kept because they are exported API, but a new
// caller should be a deliberate choice rather than an accident of name-matching.
package dmsgclient
