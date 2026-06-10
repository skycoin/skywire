# Visor-hosted skychat (`skywire cli skychat`)

By default `skywire app skychat` runs as a **child of the visor**: the
visor hands it its identity, opens its **skynet + dmsg** listeners, and
mediates pair-RPC. You don't start it by hand — you drive it with
**`skywire cli skychat`**, which talks to the app's HTTP control surface
(default **`:8001`**). This is the common, fleet-ops case.

For a chat process that runs *without* a visor (or survives visor
restarts), see [standalone-skychat.md](standalone-skychat.md).

## What the visor brings up

Launched as part of the visor's app set, `app skychat` defaults to:

- `--dmsg` **and** `--skynet` listeners **on** — messages ride either
  network using the **visor's key** (so the chat PK == the visor PK);
- the HTTP control surface on `--addr :8001` (localhost) — history, send,
  status, and the **SSE** stream the `cli` client consumes;
- pair-RPC wired to the visor (`--pair-rpc localhost:3435`) so pairing
  works (in standalone these endpoints return 503).

Operators who want it in the visor's managed app set wire it into
`skywire-config.json`'s `launcher.apps[]` (flags in the `args` array);
`skywire-autoconfig` can scaffold this.

## Sending and receiving

```bash
# send a DM (default network skynet; -w waits for a peer-receipt ack)
skywire cli skychat send -t <peer-pk> -m "hello"
skywire cli skychat send -t <peer-pk> -m "hi over dmsg" -n dmsg -w 8s

# stream incoming messages (SSE); optionally filter by sender/network
skywire cli skychat listen
skywire cli skychat listen --from <peer-pk>

# interactive split-pane TUI
skywire cli skychat chat -t <peer-pk>

# health / counters, persisted history, structured event stream
skywire cli skychat status
skywire cli skychat history
skywire cli skychat events            # NDJSON — the agent/automation surface
```

`send` notes: `-n/--net skynet|dmsg` (default `skynet`); `-w/--wait`
(default `5s`, `0` = fire-and-forget); `-r/--retries` for transient
HTTP/transport failures; `--verbose` surfaces per-layer detail to debug a
failing send; `--via tcp://<pk>@host:port` bypasses the local app and dials
a peer's chat-app directly (survives visor/dmsg outages — see
[standalone-skychat.md](standalone-skychat.md)).

`Acked by <pk> in Xms (via skynet)` confirms delivery end-to-end.

## Groups (`cli skychat group`)

Owner-centric group chat over CXO feeds:

```bash
skywire cli skychat group create --name "ops" --member <pk> --member <pk>   # --mode public|private
skywire cli skychat group invite <group>           # produces an invite link
skywire cli skychat group join   <invite-link>
skywire cli skychat group send   <group> -m "hello team"
skywire cli skychat group listen <group>           # poll-based; --interval, --since, --raw
skywire cli skychat group history <group>
skywire cli skychat group list | info <group>
skywire cli skychat group promote|demote|leave|add|delete …
```

`--mode private` encrypts feed messages with an AES key shipped inside the
invite link. The owner holds the roster; `promote`/`demote` manage admins.

## Pairing (`cli skychat pair`)

A two-key handshake with per-partner CXO pair feeds (needs the app started
with `--pair-enable`):

```bash
skywire cli skychat pair add <peer-pk>          # request
skywire cli skychat pair accept|decline <peer-pk>
skywire cli skychat pair send -t <peer-pk> -m "hi"
skywire cli skychat pair poll --since <RFC3339> --limit 100   # NO SSE — poll for replies
skywire cli skychat pair list | remove <peer-pk>
```

> **`pair poll` has no SSE.** Unlike `listen`, pair replies are not pushed —
> you must `pair poll` (or run it on an interval) to drain them.

## Aliases

```bash
skywire cli skychat alias add <pk> <name>
skywire cli skychat alias ls
skywire cli skychat alias rm <name>
```

## The SSE-hub gotcha

The control surface you talk to *is* the SSE hub. If you also run a
**standalone** skychat (its own `:8002` hub), messages don't cross between
hubs — DMs routed through the visor land on **`:8001`**, not the
standalone. Run `listen` against **both** surfaces if you operate both, and
read deduplicated by message id (SSE replays on reconnect).

## See also

- [README.md](README.md) — the model, all modes, ports, command map
- [standalone-skychat.md](standalone-skychat.md) — standalone / TCP-direct / CXO
