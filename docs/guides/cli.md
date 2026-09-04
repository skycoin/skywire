# Working with the CLI

Everything in skywire is one binary. `skywire visor` runs the visor,
`skywire cli …` drives it over local RPC (`localhost:3435` by default,
`--rpc` / `SKYWIRE_RPC` to change), and the apps, services, and dmsg
tools are subcommands of the same executable. This page collects the
conventions that apply across the whole command tree — the things that
make every other guide shorter.

For per-command flags and examples, see the generated
[command reference](../skywire/README.md). To explore interactively,
run `skywire --tui` — a browsable tree of every command with its help
text, from which any command can be run directly.

## Output: `--json`, `--jq`, `--shape`

Three global flags turn any command's output into something scriptable:

- **`--json`** — print the command's result as JSON instead of a table.
- **`--jq '<expr>'`** — filter that JSON through a
  [gojq](https://github.com/itchyny/gojq) expression (implies
  `--json`). This is built in — no external `jq` needed.
- **`--shape`** — print the output's **schema skeleton** (every field,
  zero values) instead of data. Use it to learn the exact JSON paths
  before writing a `--jq` filter; it works offline.

```
skywire cli visor info --json
skywire cli visor info --jq '.overview.local_pk'
skywire cli tp --shape          # discover the fields before filtering
```

The same `jq` engine is available as a standalone filter for arbitrary
JSON: `echo '{"a":1}' | skywire cli util jq '.a'` (flags `-r` raw,
`-c` compact, `-s` slurp).

Prefer `--jq` over grepping table output in scripts — tables are for
humans and their column layout is not a stable interface.

## One call for runtime state: `visor state`

Instead of stitching together a dozen subcommands to answer "what is
this visor doing right now", `skywire cli visor state` returns one
curated, secrets-free snapshot of the live runtime: health, routing,
transports, apps, policy, and which subsystems are wired. It is the
runtime counterpart of `config show` (which prints the on-disk
config).

The intended workflow:

```
skywire cli visor state --shape             # learn the paths (offline)
skywire cli visor state --jq '.transports'  # project what you need
```

## Driving a remote visor: `--via`

Almost any `cli` command accepts `--via dmsg://<pk>` (or
`skynet://<pk>`) to run against another visor's RPC — the full typed
API over the mesh, gated by the target's trust list. See
[Controlling a Remote Visor from the CLI](remote-visor-cli.md) for the
trust model and setup.

```
skywire cli visor info --via dmsg://<pk>
skywire cli visor state --jq '.health' --via dmsg://<pk>
skywire cli visor log --follow --min-level debug --via dmsg://<pk>
```

## Fan-out: `util foreach`

For running the same command against many targets, `skywire cli util
foreach` templates a shell command per target and runs them in
parallel:

```
# uptime of three visors, 4 at a time
skywire cli util foreach <pk1>,<pk2>,<pk3> \
    'skywire cli visor info --via dmsg://{pk} --jq .overview.local_pk'

# targets from a file, structured results
skywire cli util foreach @pks.txt 'skywire cli pty exec {pk} -- uptime' --json
```

`{pk}` is the target verbatim, `{i}` its index. `--json` emits one
NDJSON row per target with stdout/stderr/exit code/duration. Exit code
0 means every target succeeded.

## Fetching over the mesh: `got`

`skywire cli got` is an HTTP client that speaks `skynet://<pk>` and
`dmsg://<pk>` URLs in addition to `http(s)://` — routed through the
local visor, no SOCKS proxy required:

```
skywire cli got dmsg://<pk>/health -o -
skywire cli got skynet://<pk>:8080/file.bin -o file.bin
```

For browser access to the same address space, use the
[resolving proxies](resolving-proxy.md).

## Small utilities worth knowing

- `skywire cli util nc` — pure-Go netcat (`-l` listen, `-u` UDP, `-z`
  port probe). Plain TCP/UDP only; for mesh streams use
  `skywire cli dmsg cat`.
- `skywire cli util serve <dir>` — static HTTP file server on a random
  localhost port; pair with `skywire cli serve add` to expose it over
  the mesh.
- `skywire cli util edit <file>` — embedded terminal text editor with
  syntax highlighting (Ctrl+S save, Ctrl+Q quit); useful where no
  editor is installed, e.g. inside a pty session or the playground.
- `skywire cli visor pk dnslabel` — convert a public key between its
  66-char hex form and the 53-char base32 DNS label used in
  TLS-fronted `wss://` hostnames.
- `skywire cli visor doctor` — a GREEN/YELLOW/RED health rollup with a
  meaningful exit code, suitable for scripts and monitoring.

## Related

- [remote-visor-cli.md](remote-visor-cli.md) — the `--via` trust model in depth
- [transports.md](transports.md) — querying and creating transports
- [deployment-health.md](deployment-health.md) — health, pprof, and logs over dmsg
- [Command reference](../skywire/README.md) — every command, every flag
