# Hypervisor dashboard tour

The text of the tour behind the **?** button in the hypervisor UI. Edit the
wording here; `hv-tour.js` reads this file and keeps only the wiring (which page
each step opens and which element it lights up), matched by the `##` id.

How a step is written:

- `## <id>` starts a step. The id must match the script's wiring table; the
  order of the steps here is the order of the tour.
- `Title:` and `Body:` hold the text. `Title (wasm):` / `Title (native):` and
  `Body (wasm):` / `Body (native):` give different text to a browser visor and a
  native one; a plain `Title:` or `Body:` is used by both.
- `More: <label>` adds a "more" panel opened by clicking the label.
- `Notice: <label>` adds a notice opened the same way.
- A field's text runs until the next field or step. It may use `**bold**`,
  `*italic*`, `` `code` ``, lines starting with `- ` for a list, and a blank
  line for a paragraph break.

The copy narrates rather than instructs: it describes the screen ("Each row is
a visor"), never the reader ("you can see your visors here").

## visor-list

Title (wasm): A hypervisor dashboard, served from a tab
Title (native): The hypervisor dashboard

Body (wasm):
A **hypervisor** manages visors: it lists them, opens them and configures them.
This one is ordinary web software with one oddity — the visor it manages is the
**wasm core in this tab**, and the tab is also serving the page. Nothing was
installed and nothing persists: closing the tab ends the visor unless its key
was exported. The list below reads left to right.

Body (native):
A **hypervisor** manages visors: it lists them, opens them and configures them.
It is itself a visor that serves this page and holds the keys of the visors it
manages. The list below reads left to right.

Notice: Open-source, no warranty — keys are held locally
Skywire and the Skycoin wallet are experimental, open-source software, provided
as-is and without warranty. Visors hold their own keys and coins; nobody else
can access or recover them. Understand the risks before relying on it or
storing value.

## key

Title: A visor is a public key

Body (wasm):
Each visor is named by a **public key** — 66 hex characters. There are no
usernames and no accounts: the key is both identity and address. The secret
half sits in **this browser** and never leaves it; every link the visor makes is
encrypted to it.

Body (native):
Each visor is named by a **public key** — 66 hex characters. There are no
usernames and no accounts: the key is both identity and address. The secret
half sits **on this machine** and never leaves it; every link is encrypted to
it.

More: Why a key instead of a name or an address
On the clearnet an address locates a host and a certificate authority vouches
that a name belongs to it — two mechanisms, two trust assumptions. A public key
collapses both: it is what peers route to and what they encrypt to. There is no
name to spoof and no authority to trust. Whoever holds the matching secret key
*is* that visor.

## dmsg

Title: dmsg — the relay layer

Body (wasm):
Connected **dmsg servers**, counted by carrier. dmsg is an encrypted relay
network: two visors reach each other through a server instead of directly. A
browser cannot open a raw socket, so it joins over **WSS** and
**WebTransport**.

Body (native):
Connected **dmsg servers**, counted by carrier. dmsg is an encrypted relay
network: two visors reach each other through a server instead of directly. A
native visor joins over **TCP**, or QUIC where a server offers it.

More: The four carriers
dmsg carries the control plane, and a data path when no direct one exists. The
**carrier** is how a visor reaches a server:

- **tcp** — a raw socket; native only.
- **ws** / **wss** — WebSocket; the only option in a browser. An https page requires `wss`.
- **webtransport** — HTTP/3; dialable from a browser.
- **quic** — QUIC over UDP; native. The only carrier that also passes unreliable **datagrams**.

The next column counts **direct transports**, which use the same four protocols
peer-to-peer. dmsg is the relayed form of the same wire.

## transport-counts

Title: Transports — the direct links

Body:
Live **transports**, by type. A transport is point-to-point where dmsg relays.
The count climbs as a visor settles in and finds peers; more direct links mean
less relaying and lower latency.

More: Transport types
Four mirror the dmsg carriers exactly — same protocol, dialed to a peer instead
of a server:

- **stcpr** — TCP, with the peer located through the address resolver.
- **ws** — WebSocket.
- **webtransport** — HTTP/3.
- **quic** — QUIC over UDP; carries end-to-end datagrams.

Two exist only peer-to-peer, since a dmsg server is a fixed public endpoint with
nothing to hole-punch to:

- **sudph** — UDP through a NAT hole-punch, made reliable by KCP.
- **webrtc** — browser-to-browser DataChannel, traversed by ICE; dmsg carries only the signaling.

dmsg is itself a transport — the relayed one.

## label

Title: Label and location

Body:
A **label** for recognizing a visor at a glance, and its detected IP and
country. Both are cosmetic; the public key is the identity. That is the whole
row — which visor, how it connects, and where it is.

## info

Title: Info

Body (wasm):
Opening a visor lands on **Info**: version, uptime, identity, and the **DMSG
servers** line the list summarized — here reached over WSS and WebTransport
from the tab.

Body (native):
Opening a visor lands on **Info**: version, uptime, identity, and the **DMSG
servers** line the list summarized — the relay layer up close.

## transports

Title: Transports

Body (wasm):
One row per direct link. From a browser these are mostly WebTransport and
WebRTC; a native visor also builds stcpr, sudph and quic.

Body (native):
One row per direct link — stcpr, sudph, dmsg, quic — hole-punched through NATs
where necessary, so traffic takes the shortest path rather than always
relaying.

## routing

Title: Routing

Body:
The paths traffic takes across those transports. A route is **DIRECT** (one
hop), **MULTIHOP** (several visors, so no single hop sees both ends), or
**MULTIPLEXED** (parallel paths at once). The route-finder builds them on
demand.

More: Hops, and what they cost
A route is an ordered path of transports. One hop is fastest. More hops mean no
single intermediary knows both source and destination, paid for in latency. A
multiplexed route splits one stream over several paths: a dead leg does not end
the stream, and the aggregate beats any single leg.

## bandwidth

Title: Bandwidth

Body:
What those routes actually carried, sent and received over time — the quickest
way to tell a working transport from a merely connected one.

## apps

Title: Apps

Body (wasm):
The apps this visor can run. In a tab the relevant one is
**skysocks-client-lite**, which routes clearnet fetches out through an **exit**
visor. It serves no local port — it exists only inside this tab, and only this
tab's own traffic goes through it.

Body (native):
Apps start, stop and configure here: the **skysocks** proxy client and server,
the **VPN** client and server, and **skychat**. On a native visor these bind
real local ports and can serve other machines on the network.

More: skysocks-client and skysocks-client-lite
**skysocks-client** runs as a process and serves a local **SOCKS5 port** that
other programs point at. **skysocks-client-lite** has neither: it lives in the
tab and proxies only what that tab fetches. Both dial an **exit** visor which
performs the clearnet request, so the site sees the exit's address.

## mail

Title: Mail

Body:
Skymail, the visor's own mailbox. Mail is addressed to a visor's public key and
delivered straight to that visor over dmsg or skynet, so there is no account and
no mail server.
An empty whitelist accepts mail from any visor. Once it lists keys, only those
keys can deliver.

## terminal

Title: Terminal

Body (wasm):
A shell into this visor. In a tab it is websh, a shell running inside the wasm
core, with the `skywire` command built in.

Body (native):
A shell on the machine running this visor, over the visor's own pty. The same
terminal opens on any visor this hypervisor manages.

## uptime

Title: Uptime

Body:
This visor's record of staying online and reachable, day by day. The reward
system pays against that record.

## logs

Title: Logs

Body (wasm):
A live tail of the visor's runtime — dmsg, transports, routing, apps — from the
wasm core in this tab.

Body (native):
A live tail of the visor's runtime — dmsg, transports, routing, apps — read
from the visor process.

## visor-settings

Title: Visor settings

Body:
One visor's label, reward address and mesh behavior. Hypervisor-wide settings
are a separate page, at the end of this tour.

## cluster

Title: The cluster

Body (wasm):
Back at the top level. Every visor attached to this hypervisor appears here — at
the moment one, in this tab. A remote visor joins the list when its public key
is added; this visor becomes managed elsewhere when a remote hypervisor's key is
set in its config.

Body (native):
Every visor attached to this hypervisor appears here. A remote visor joins the
list when its public key is added; this visor becomes managed elsewhere when a
remote hypervisor's key is set in its config. Each one is fully controllable
over the mesh.

More: Which is the hypervisor
A hypervisor is a visor that also serves this UI and holds the keys of the
visors it manages. The relationship is one config entry, in one direction or
the other. Management travels the same encrypted mesh as everything else, so a
hypervisor in a browser tab can drive a native visor on the other side of the
world, and the reverse.

## mesh-transports

Title: Every transport on the mesh

Body:
The live edge list the route-finder draws on: every direct link across the
whole mesh, with per-type bandwidth. Assembled **peer-to-peer over dmsg**, not
fetched from a web server. On a large deployment this page is heavy and takes a
while to settle.

## mesh-visors

Title: Every visor on the mesh

Body:
A searchable directory of the whole mesh, with the running count top left.
Filters by country, version and transport type; each entry carries its
transport mix and uptime. The next page draws the same set as a graph.

## visualizer

Title: The same graph, drawn

Body:
An interactive geographic render of that directory, in flat, globe and WebGL
views.

## services

Title: Shared services

Body (wasm):
Health of the services the mesh depends on: config, dmsg discovery, transport
discovery, the route-finder, the address resolver, service discovery. **This
tab probes each one directly over dmsg** — status, version, latency — with the
same reach a native visor has.

Body (native):
Health of the services the mesh depends on: config, dmsg discovery, transport
discovery, the route-finder, the address resolver, service discovery. Each is
probed over dmsg, visor to visor.

## mesh-uptime

Title: Mesh uptime

Body:
How consistently every visor has stayed online, over 1, 7 and 30 days — the
mesh-wide form of a single visor's uptime tab, and the basis for rewards.

## rewards

Title: Rewards

Body:
Skycoin paid out to visors that stay online and reachable; a visor appears once
it qualifies. The page is hidden on a browser visor, which cannot hold up an
uptime record across a closed tab.

## resources

Title: Host resources

Body:
CPU, memory and disk of the machines hosting the managed visors. Also hidden on
a browser visor, which has no host to measure.

## hv-settings

Title: Hypervisor settings

Body:
The services this deployment points at, the update channel, and how the UI
behaves — applying to every visor this hypervisor manages.

## close

Title: Run by nobody

Body (wasm):
No account, no server, no address handed down — a global peer-to-peer mesh
reachable from a browser tab, and gone when the tab closes unless the key was
exported.

That was the **dashboard**. The desktop around it — the mesh browser, the shell,
files, identity, pairing, mail — is the genuinely unusual part, and it has **its
own tour** in the launcher. This one reopens from the **?** button.

Body (native):
No account, no central server, no address handed down — cryptographic keys and
a global peer-to-peer mesh.

That was the **dashboard**: the visor list, one visor up close, and the
mesh-wide views. It reopens from the **?** button.
