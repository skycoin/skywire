# Skywire PTY

A **pty** (pseudoterminal) gives you an interactive remote shell, one-shot
command execution, and a remote filesystem mount on another skywire
node — addressed by **public key**, secured by a **noise-XK** handshake.
No SSH is involved; the `host` / `shell` / `fs` names parallel
`sshd` / `ssh` / `sshfs` only by analogy.

This is one subsystem reachable through **two command structures that are
converging**:

- **`skywire cli pty …`** — the operator/client surface, and the *only*
  way to drive the **visor-hosted** pty.
- **`skywire app pty …`** — the pty as a native app: the **standalone
  servers** plus the browser (`http`) and `exec` clients.

A third tree, **`skywire dmsg pty …`**, is the *same commands* as
`app pty`, kept mounted but **hidden** in the default build. It exists so
the dmsg command-structure can be compiled as its **own standalone
binary** (a pure `dmsgpty` with no visor/skywire deps). In the main
`skywire` binary it's hidden to funnel operators to `app pty` — see
[Redundant / equivalent commands](#redundant--equivalent-commands).

## The three modes

| Mode | Identity | Transport | Run / reach it with | Use when |
|------|----------|-----------|---------------------|----------|
| **1 — visor-hosted** | the **visor's** key | skynet **or** dmsg | `skywire cli pty …` (server is the running visor; not under `app pty`) | the node runs a visor — the default, fleet-ops case |
| **2 — standalone dmsg** | its **own** keypair (**cannot** borrow the visor's) | dmsg (+ optional TCP) | `skywire app pty dmsg` (≡ `dmsg pty host`) | a box with no visor, or one that mustn't contend with the visor's dmsg entry |
| **3 — standalone TCP** | **can use the visor's PK** (`--sk-from-visor`) | direct TCP (noise-XK) | `skywire app pty tcp` (≡ `dmsg pty host --no-dmsg --tcp-listen`), or operator-friendly `skywire cli pty host` | you want a process **independent of the visor's lifecycle** — survives visor restarts, so it's the right tool to **update skywire itself** or as a plain ssh-equivalent |

Detailed docs:

- **[skywire-pty.md](skywire-pty.md)** — mode 1 (visor-hosted) and the
  `cli pty` client commands. **Start here.**
- **[standalone-pty.md](standalone-pty.md)** — modes 2 & 3 (the standalone
  servers) plus the `http` and `exec` clients, via `app pty`.

## The trust model (identical everywhere)

Every mode and every transport uses the same authorization:

| SSH concept            | Skywire pty equivalent                                       |
|------------------------|--------------------------------------------------------------|
| host-key check         | noise **XK** pins the **server PK** from the destination     |
| `authorized_keys`      | the host's **whitelist** of client PKs                       |
| password / pubkey auth | the **client PK alone** (from the local visor's SK by default)|

The server's own PK is always implicitly allowed; for a visor, its
**hypervisors** and `pty.whitelist` are allowed too. One whitelist gates
dmsg, TCP, and the gated web UI alike. (The standalone `http` browser
bridge is the exception — it's a *local* socket bridge with no PK gate;
secure it by not exposing its port.)

## Things built on the pty

- **`fs` — the `sshfs` equivalent.** `cli pty fs mount <pk>@<host>:<port>
  <dir>` mounts a peer's filesystem over the pty subsystem's sftp channel
  (Linux + FUSE). Same PK/whitelist trust model as a shell.
- **`exec` — the agent-friendly path.** `cli pty exec <pk> -- <cmd>`
  captures stdout/stderr/exit-code with no TTY — the form scripts and
  automated agents use to drive a fleet. Mirrors the remote exit code
  locally (`124` on timeout).
- **`http` — browser terminal.** A WebSocket terminal. The visor serves
  one at `/pty` (whitelist-gated); the standalone `app pty http` bridges a
  browser to a *local* host socket (see [standalone-pty.md](standalone-pty.md)).

## Full command map

### `skywire cli pty …` — operator / client (and visor-hosted access)
| Command | Role |
|---------|------|
| `cli pty exec <pk> -- <cmd>` | one-shot exec (agent-friendly), via local-visor RPC or `--via` TCP |
| `cli pty start <pk>` | interactive shell via the visor |
| `cli pty shell <pk>@<host>:<port> [-- <cmd>]` | ssh-shaped direct-TCP shell/exec |
| `cli pty fs mount` / `fs umount` | sshfs-shaped FUSE mount |
| `cli pty list` | peers the local visor sees |
| `cli pty ui` / `cli pty url` | open / print the visor's web-terminal URL (can target a remote visor per-PK) |
| `cli pty host` | run a TCP-only (mode-3) server — `--listen`/`--allow`/`--sk-from-visor` |

### `skywire app pty …` — the native app (standalone servers + clients)
| Command | Role | Hidden alias |
|---------|------|--------------|
| `app pty dmsg` | **mode 2** server (dmsg; `--tcp-listen`/`--no-dmsg`) | `dmsg pty host` |
| `app pty tcp` | **mode 3** server (TCP-only, `--addr`) | `dmsg pty host --no-dmsg --tcp-listen` |
| `app pty http` | browser bridge to a **local** host socket | `dmsg pty ui` |
| `app pty exec` | standalone client (interactive) | `dmsg pty cli` |
| `app pty exec exec <pk> -- <cmd>` | standalone one-shot exec | `dmsg pty cli exec` |
| `app pty exec whitelist` | list a host's whitelist | `dmsg pty cli whitelist` |
| `app pty exec whitelist-add <pk>…` | add to a host's whitelist | `dmsg pty cli whitelist-add` |
| `app pty exec whitelist-remove <pk>…` | remove from a host's whitelist | `dmsg pty cli whitelist-remove` |
| `app pty dmsg confgen` | scaffold a standalone host config | `dmsg pty host confgen` |

## Redundant / equivalent commands

Because the two structures are converging, several commands are the same
code at different paths. Prefer the left column:

| Canonical (`app pty` / `cli pty`) | Hidden legacy (`dmsg pty`) | Notes |
|-----------------------------------|----------------------------|-------|
| `app pty dmsg` | `dmsg pty host` | identical |
| `app pty tcp` *or* `cli pty host` | `dmsg pty host --no-dmsg …` | `cli pty host` is the operator-friendly mode-3 server (`--allow`, `--sk-from-visor`); `app pty tcp` is the app-framework one. Same protocol. |
| `app pty http` | `dmsg pty ui` | identical |
| `app pty exec [exec\|whitelist*]` | `dmsg pty cli [exec\|whitelist*]` | identical |

The `dmsg pty` tree is `Hidden = true` in the main binary
(`cmd/skywire/commands/root.go`) but still callable — and it's the
*visible* surface if you compile the dmsg structure as its own binary.

**No longer present:** `cli sshd` / `cli ssh` (folded into `cli pty host`
/ `cli pty shell`); `cli dmsg pty …` (the pty tree moved to `cli pty`).

## See also

- [skywire-pty.md](skywire-pty.md) — visor-hosted mode + `cli pty`
- [standalone-pty.md](standalone-pty.md) — standalone dmsg / TCP servers + http/exec clients
