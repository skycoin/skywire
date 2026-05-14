# skywire dmsg

DMSG (Direct Messaging) is Skywire's relay-based encrypted messaging
network. The `skywire dmsg` umbrella exposes the dmsg sub-binaries
(dmsg-server, dmsg-discovery, dmsghttp, dmsgcurl, dmsgweb, dmsgpty,
self-ping, etc.) under one cobra tree.

Command-line documentation lives at
[/docs/skywire/dmsg/README.md](../../docs/skywire/dmsg/README.md) and is
generated from the live cobra tree — run `skywire doc` from the repo
root to regenerate.

Per-binary deep documentation (HTTP endpoints, protocol notes, etc.)
remains under each sub-binary's own directory, for example
[dmsg-discovery/](dmsg-discovery/README.md) or
[dmsgweb/](dmsgweb/README.md).
