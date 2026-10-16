# The browser visor

A Skywire visor can run in a browser tab with nothing installed. It is the
same `skywire` program as the native visor, built for `GOOS=js GOARCH=wasm`,
and it joins the same network with its own key. Open
[skywire.dev](https://skywire.dev) to start one.

This page explains how the pieces fit together. The pages it links to cover
each piece in detail.

## What runs where

| Piece | Where it runs | What it does |
|---|---|---|
| The page | the tab | loads the desk, holds the window manager, forwards deep links |
| The visor | a worker the page starts (the exec worker) | runs `skywire autoconfig` and then the visor, as on a computer |
| vnet | page-wide | a virtual loopback network, so `127.0.0.1:<port>` works inside the tab |
| The vnet service worker | the browser | lets the page load `http://vnet:<port>/` pages that the visor serves on vnet |
| The filesystem | page-wide, in memory | what the visor, the shell and the file manager see as their disk |

The visor writes its config and keys to that in-memory filesystem. The tab
saves a snapshot of it to IndexedDB (database `skywire-desk`), so the same
key comes back on the next visit. Clearing the site data gives the tab a new
key. [hypervisor.md](hypervisor.md#resetting-a-tabs-identity) shows how.

Only one tab per site runs the visor. A second tab on the same site shows a
notice instead of starting a second visor with the same key, and passes its
link to the tab that is running.

## Getting on the network

A browser cannot open raw TCP or UDP sockets, so a browser visor connects
with what a page can use:

- dmsg over WebSocket, to the dmsg servers;
- WebSocket and WebTransport transports to visors that accept them;
- WebRTC transports to other browser visors and to visors that accept WebRTC.

It cannot accept stcpr or sudph transports, so other visors cannot dial it
directly. The [host bridge](host-bridge.md) changes that. It is a small
program the visor hands out from its own dashboard, and it gives the tab the
computer's sockets: stcpr and sudph on real ports, and port forwards so
programs on the computer can use the tab's proxy.

## The desk

The desk is the tab's desktop: a panel, an applications menu and windows.
[wasm-visor-ui-tour.md](../wasm-visor-ui-tour.md) walks through every window.
The main ones are:

- **Dashboard**: the same Angular hypervisor UI a native visor serves, loaded
  from the tab's visor through the vnet service worker.
- **Terminal**: websh, a bash-like shell over the tab's filesystem. The whole
  `skywire` CLI runs in it, and `curl` reaches `.dmsg` and `.skynet` names.
- **Browser**: opens `.dmsg` and `.skynet` sites and, through a proxy, the
  rest of the web. See [skynet-browser.md](../skynet-browser.md) and
  [real-origin-browser.md](../real-origin-browser.md).
- **Chat, mail, files** and the network graph.

The same desk can also be served by a native visor next to its dashboard.
Then it drives that visor instead of running its own.

## Links that open something

A link to the desk can say what to open:

| Link | Opens |
|---|---|
| `https://skywire.dev/?skynet=<name>.skynet` | that mesh site in the desk browser |
| `https://skywire.dev/?dmsg=<name>.dmsg` | the same, for a `.dmsg` name |
| `…&kiosk=1` | the site maximized |
| `https://skywire.dev/?skydm=<pk>` | a chat with that visor |
| `https://skywire.dev/?skygroup=<invite>` | the chat join panel with that invite |

The value is a full name with its suffix, for example `?dmsg=rewards.dmsg`.
The same keys also work after `#`.

## Where the module comes from

The module is about 38 MB and is not in the packages. When a desk is opened,
the serving visor refreshes it from the deployment's source visor over dmsg,
or installs the release's copy over HTTPS when it has none. `skywire cli hv
serve` serves a standalone desk the same way.

## Other things the tab visor runs

- A Wisp server, so an emulator in the page, such as LinuxOnTab, can use the
  visor's network. [linuxontab.md](linuxontab.md) shows how.
- The resolving proxies, so the desk browser and `curl` in the terminal reach
  mesh names.
- Its own mailbox, chat and SOCKS5 client, as a native visor does.

## Limits

- Inbound transports need the host bridge.
- It cannot be an exit for others.
- Its VPN client covers the tab, not the computer.
- It runs while the tab is open.
- The `wasm` architecture is left out of rewards.

## See also

- [host-bridge.md](host-bridge.md): the computer's sockets for a tab
- [mesh-addresses.md](mesh-addresses.md): the names a browser visor opens
- [hypervisor.md](hypervisor.md): pairing a tab with a computer
- [glossary.md](../glossary.md#component-libraries): the libraries the tab is built from
