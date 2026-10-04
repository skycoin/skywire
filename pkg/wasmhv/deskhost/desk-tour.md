# Desk tour

The text of the tour the desk's launcher opens as the app `tour`. Edit the
wording here; `desk_tour_js.go` embeds this file when the binary is built and
keeps only the wiring (the app each step opens), matched by the `##` id.

How a step is written:

- `## <id>` starts a step. The id must match the wiring in `desk_tour_js.go`;
  the order of the steps here is the order of the tour.
- `Title:` and `Body:` hold the text. A field's text runs until the next field
  or step.
- The text may use `**bold**`, `*italic*`, `` `code` ``, lines starting with
  `- ` for a list, and a blank line for a paragraph break.

The copy narrates rather than instructs — it describes what a thing is, not
what the reader should do with it. This tour carries the claim the dashboard
tour deliberately does not make: a visor running in a browser tab, with a
desktop around it, is an unusual thing. The Angular dashboard next door is an
ordinary admin console and says so.

## intro

Title: A visor, running in a browser tab

Body:
Not a page describing Skywire, and not a remote session: the **wasm binary
serving this page is a full visor**, routing on the mesh from inside this tab.
Around it is a **desk** — a real window manager whose windows move, resize,
dock and stack. Nothing was installed. Nothing is running on a server on the
reader's behalf. Everything in the following windows executes here.

## launcher

Title: The launcher

Body:
Every app opens from the **launcher** in the taskbar, this tour included, so it
can be closed and reopened. From here each step **opens the app it describes**,
beside this window. Windows that get moved or resized are left alone
afterwards; untouched ones are tidied up.

## browser

Title: browser — two networks at once

Body:
**netscrape** resolves two kinds of address. A `<pk>.dmsg` address fetches a
site directly from another visor over dmsg: no DNS, no certificate authority,
the **public key is the address and the authentication at once**. A clearnet
address is fetched by an **exit visor** instead — selected automatically unless
one is pinned — so the site sees the exit's address. Names like `skywire.dmsg`
are a local convenience, not a namespace anyone can squat.

## dashboard

Title: The dashboard is a tab in it

Body:
The **hypervisor dashboard** — visor list, transports, routing, the mesh-wide
views — is not a window here. It is an Angular app this visor serves on its
virtual loopback, opened as a native tab at `vnet:8001`. That is the seam. It
is ordinary admin software, which is why it gets a separate, plainer tour,
behind the **?** button in its bottom-right corner.

## console

Title: console — a shell with no server

Body:
The console is **websh**, running in the same wasm runtime as the visor. There
is no host on the other end of it. Pipes, globbing, control flow, job control,
`jq` and `awk` all work, and the visor's own commands emit JSON into the
pipeline — the same scripting surface `skywire cli` gives a native visor.

## files

Title: files — one filesystem, two views

Body:
The file browser and the shell share a single in-memory filesystem.
`echo hi > /notes.txt` in the console appears here; an edit here is visible to
`cat`. Nothing touches the host disk, and like the tab itself it is ephemeral
unless exported.

## mail

Title: mail — addressed by key

Body:
A mailbox whose address is a **public key**, delivered across the mesh rather
than through a provider. There is no account to register and no server holding
the messages; a whitelist decides who can deliver.

## identity

Title: identity — the key is the visor

Body:
This tab's visor *is* a keypair, and this is where it lives. **Export** backs
it up or moves the visor to another device; importing one and reloading
restarts the visor under that key. The secret half leaves the tab only as
copied text. There is nobody to recover it from.

## pair

Title: pair — driving the host visor

Body:
Pairing asks the visor serving this page to accept **this tab** as its
hypervisor. The operator approves a fingerprint shown by
`skywire cli visor hv pair`, or hands over a one-time code. After that the
dashboard tab is managing a real visor on the host.

## settings

Title: settings — what each reload builds

Body:
This tab's `skywire.conf`: the services it points at and the options every
reload generates the visor's config from. A browser visor is rebuilt from this
file on each start, so changes have to land here to survive one.

## install

Title: install — surviving the address

Body:
Installed as an app, the desk opens on its own from the browser's storage and
keeps working when the address that served it is unreachable. For a tool whose
job is browsing a mesh, not needing the network in order to start is most of
the point.

## close

Title: Ephemeral by default

Body:
No install, no account, no server: a visor, a desktop, a shell, a browser and a
mailbox in one tab, and all of it gone when the tab closes unless the key was
exported or the desk installed.

This tour reopens from the launcher. The **dashboard** has its own, behind the
**?** button in its corner.
