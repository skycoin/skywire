---
name: skywire-pty
description: The skywire pty subsystem. Remote command execution, interactive shell and filesystem mount over dmsgpty, the transport choice (auto, dmsg, skynet, tcp), whitelist auth and remote ops traps. Load for running commands on remote visors or remote debugging.
---

# skywire pty, the ssh of skywire

Key addressed remote access over the dmsgpty subsystem. It is noise authenticated and gated by the remote visor's dmsgpty whitelist. Commands run as the remote visor's user. Everything is under `skywire cli pty`. Use `--help` for the flags. `skywire cli dmsg scp` is the scp and `pty fs mount` is the sshfs.

## Commands
- `pty exec <pk> -- <cmd...>` runs one command with no TTY. Stdout and stderr come back, and the remote exit code is returned. The host caps output at 16 MiB and runtime at 5 minutes. Use `-t` or `--timeout`, `-e KEY=VAL` for environment. For pipelines use `-- /bin/sh -c '<script>'`.
- `pty start <pk>` is an interactive shell over dmsg and needs a TTY.
- `pty shell <pk>@<host>:<port> [-- <cmd>]` is a shell or exec over direct TCP to a `pty host` listener.
- `pty host` runs a direct TCP pty server. `pty list` shows connected visors. `pty ui` and `pty url` give the web UI.
- `pty fs mount <pk>[@host:port] <mountpoint>` mounts a peer filesystem with sftp and FUSE. `pty fs umount <mountpoint>` removes it.

## Transport
Exec, start, shell, fs and `dmsg scp` take `--transport auto|dmsg|skynet|tcp`. A target can also select it: `dmsg://<pk>`, `skynet://<pk>` or `tcp://<pk>@host:port`. A scheme that disagrees with `--transport` is an error.
- exec supports all four. `auto` tries skynet first and falls back to dmsg.
- shell is direct TCP only. start supports auto, dmsg and tcp. fs mount supports auto, dmsg and tcp, and skynet is not wired.
- `dmsg` needs no route or transport setup. It is the best choice for one off commands and for fan out to many hosts. Pass it explicitly, because `auto` picks skynet.
- `skynet` rides a route. It is fast only when the route is one direct transport of a solid type (stcpr, sudph, stcp). Over a multi hop route, a webrtc hop or a dmsg transport it can take tens of seconds.
- When dmsg times out and a direct transport exists, try `skynet://<pk>` before raising the timeout.
- Check for a direct transport with `skywire cli tp -p <pk>`. Create one with `skywire cli tp add <pk> --type stcpr`.
- If you cannot dial the remote because of NAT, ask the remote to dial you. `skywire cli tp add --remote <remote-pk> <local-visor-pk> --type stcpr` goes through the Transport Setup Node. See skywire-transport-setup.
- The auto, dmsg and skynet paths go through the local visor RPC, so the local visor must be up. Only direct TCP bypasses it.

## Keys on direct TCP
The noise key comes from `--sk` or `DMSGPTY_SK`, then the borrowed visor key when `--visor-key` is set, then a random one.
- `shell` and `fs` borrow the visor key by default.
- `exec` and `start` do not. Running `pty exec --via tcp://<pk>@host:port` without `--visor-key` uses a random key and a whitelisted host rejects it with `NewPtyClient: failed to read response: EOF`. Add `--visor-key` or `--sk <whitelisted key>`.

## Traps
- `pty exec` does not forward stdin. A pipe into it goes nowhere. To send a small file, pass it as base64 inside the command: `pty exec <pk> -- /bin/sh -c 'echo <b64> | base64 -d > /path'`. For bigger files use `skywire cli dmsg scp`.
- `pty fs mount` without `--ro` is writable, so you can copy files in. It runs in the foreground, so background it or use another terminal.
- A mount that failed leaves an empty local directory. Before writing into the mountpoint, check `mountpoint <dir>`, or you will write into the empty local directory.
- Unmount with `pty fs umount` before restarting the local visor. The via-visor mode breaks when the visor halts.
- `--remote-root <path>` limits and speeds up a mount, and is the usual way to read remote logs.
- `pkill -f "<pattern>"` inside `pty exec` matches the exec's own command line and kills your session. Split the pattern (`pkill -f "sky""net"`) or kill by PID.
- A process started with `&` inside `pty exec` dies when the session ends. Use `systemd-run --unit=<name> --collect <cmd>` or `setsid`.
- Empty output or a timeout usually means a slow fallback, a busy remote, or a race with a background launch. Retry, then change transport.
- Do not run many `pty exec` in parallel from one host. Go sequentially, or dedupe hosts first with `skywire cli visor hv ls`.

## Recipes
- `pty exec <pk> --transport dmsg -- /bin/sh -c 'grep -vE "^\s*#|^\s*$" /etc/skywire.conf'`
- `pty exec <pk> --transport skynet -- journalctl -u skywire --since '5 min ago'`
- `skywire cli dmsg scp <pk>:/remote/file ./file` and `skywire cli dmsg scp ./file <pk>:/remote/file`. `scp --transport dmsg` with a random key is rejected by the remote whitelist, so use `auto` or a whitelisted `--sk`.
