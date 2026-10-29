---
name: skywire-transport-setup
description: Create and remove skywire transports, on the local visor (tp add / tp rm), and on a REMOTE target visor via the Transport Setup Node (tps add/list/rm, or tp --remote / tp rm --remote). Load when asked to establish a transport between two visors, tear one down, wire up a peer remotely, or toggle public autoconnect.
---

# skywire transport setup (local + remote via Transport Setup Node)

A transport is created by ONE edge dialing the other, then is bidirectional. Types: **stcpr, sudph, dmsg, squicr, webrtc**. To VIEW transports, use the **skywire-transports** skill.

## Local: create / remove a transport FROM this visor
- `skywire cli tp add <remote_pk> -t <type>`, this visor dials + establishes a transport to `<remote_pk>`. **The PK is POSITIONAL** (one or more). `-t/--type` (stcpr/sudph/dmsg/...), `-o/--timeout <dur>`, `-n/--retries <N>`. ⚠ There is also a `-r/--rpk` flag but it does NOT satisfy the required positional (`tp add -r <pk>` alone errors "requires at least 1 arg"), **always pass the PK positionally**.
  - `skywire cli tp add edge <pk>`, establish transports across all edges of a PK (slow, dials several).
  - `skywire cli tp add pv`, establish transports to public visors in bulk (slow, long-running; overlaps with autoconnect).
- `skywire cli tp rm -i <transport_id>`, remove ONE local transport. **The ID goes to `-i/--id`**, a bare `tp rm <id>` positional just prints help.
  - `skywire cli tp rm -a` / `--all`, **remove ALL transports (destructive)**, do not run casually on a live visor.

## Remote: act on ANOTHER visor via the Transport Setup Node (TPS)
`skywire cli tps` asks a *target* visor to add/list/remove transports. **The target must trust THIS visor's TPS public key** (whitelist), else the request is rejected. (A visor trusts itself, so `-t <own-pk>` works for testing.)
- `skywire cli tps list -t <target_pk>`, list the target's transports (ID · LOCAL · REMOTE · TYPE).
- `skywire cli tps add -t <target_pk> -r <remote_pk> -T <type>`, ask `<target>` to create a transport to `<remote>`. `-T/--type` default `stcpr`. Use to wire two REMOTE visors, or to have a peer dial you.
- `skywire cli tps rm -t <target_pk> -i <transport_id>`, ask the target to remove a transport. **Uses `-i`** (NOT `--tp`).
- `--via dmsg://<pk>` / `skynet://<pk>` on any `tps` cmd routes the control request through a specific visor.

There is a SECOND spelling for remote-remove on the `tp` command (equivalent, different flags, a known inconsistency): `skywire cli tp rm --remote <target_pk> --tp <id>[,<id>]` → "OK - removed N transport(s) on M visor(s)". Prefer `tps rm -t/-i` for clarity.

## Autoconnect
- `skywire cli tp auto`, show status; `tp auto on` / `tp auto off`, start/stop public autoconnect. Verified toggles cleanly ("running" ⇄ "stopped"). Autoconnect is the normal way a fresh visor populates transports to public visors without manual `tp add`.

## Deterministic transport ID
- `skywire cli tp id <pk1> <pk2> -t <type>`, compute the UUID a (PK-pair, type) will get (verified: matches the live ID). Re-adding the same pair+type is idempotent on the ID.

## Gotchas
- **Trust:** `tps add/rm` (and `tp rm --remote`) fail unless the target whitelists this visor's TPS PK. Local `tp add`/`tp rm` need no such trust.
- **Type reachability:** `sudph`/`squicr` can fail if the remote's AR hole-punch/QUIC bind is down; `stcpr` is the most reliable, `dmsg` is always establishable (rides dmsg servers).
- New transports feed the local router immediately, but route-finder visibility for OTHERS depends on TPD publish convergence (~minutes after establishment; ~10 min to fully re-publish after a visor restart).
