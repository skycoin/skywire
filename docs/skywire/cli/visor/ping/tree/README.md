# skywire cli visor ping tree

[← skywire cli visor ping](../README.md)

Ping visors via transport routes with a scrollable terminal UI.

This command uses a Bubble Tea-based TUI that lets you scroll through
results while the ping test runs.

Controls:
  ↑/k, ↓/j     Scroll up/down one line
  PgUp/PgDn    Scroll up/down one page
  Home/End     Go to top/bottom
  q/Ctrl+C     Quit

The display updates live while preserving your scroll position.

Level vs hops:
  --max-level N    cap the BFS depth — ping levels 1, 2, ..., N
                   (0 = unlimited until no new visors discoverable)
  --hops N         ping ONLY the visors exactly N hops away from us
                   (use with --max-level >= N so discovery reaches them)
  default of both means "ping every level reachable through direct
  transports and their neighbors, until expansion exhausts."

Most operators want --max-level. --hops is for targeted measurement
when characterizing latency-by-hop-count.

## Usage

```
skywire cli visor ping tree
```

## Examples

```
# Ping every visor reachable via direct transports (level 1 only),
  # with 5 latency samples per transport and only "online" peers.
  skywire cli visor ping tree2 --max-level 1 --tries 5 --online

  # Discovery + ping out to 3 hops; useful for the "latency as a
  # function of hop count" measurement Synth asked about.
  skywire cli visor ping tree2 --max-level 3 --tries 10 --online \
    -O ping-3hop-$(date +%F).json

  # Show ONLY what would be pinged (the BFS discovery tree), without
  # firing any actual pings. Quick way to inventory your reachable
  # network before committing to a long run.
  skywire cli visor ping tree2 --max-level 3 --dry-run

  # Resume a long run that was interrupted (re-uses the same -O file).
  skywire cli visor ping tree2 --max-level 3 --tries 10 --resume \
    -O ping-3hop-$(date +%F).json

  # DMSG-only measurement (skip route-based ping; just probe DMSG
  # server reachability). Useful for diagnosing route-setup-node
  # issues separately from transport-level connectivity.
  skywire cli visor ping tree2 --dmsg-only --online --tries 10

  # Filter to visors running v1.3.51 or newer (skips old visors
  # whose latency-publish path is broken).
  skywire cli visor ping tree2 --max-level 2 --version v1.3.51
```

## Flags

```
  -m, --cfa int                  update cache files if older than n minutes (default 5)
      --cfd string               DMSG clients cache file location (default "/tmp/dmsg-clients.json")
      --cft string               TPD cache file location (default "/tmp/tpd.json")
      --cfu string               UT cache file location (default "/tmp/ut.json")
  -c, --concurrency int          max concurrent ping operations (default 2)
      --continuous               run continuously, re-checking trees
      --dmsg                     pre-check visor reachability over DMSG before route ping
      --dmsg-all-servers         ping via all DMSG servers (not just first success)
      --dmsg-only                ping via DMSG servers instead of routes
      --dmsgurl string           DMSG discovery URL (default "http://dmsgd.skywire.skycoin.com")
      --dry-run                  show tree structure without pinging
      --hops uint                exact hop level to ping (0 = all levels)
      --max-age duration         re-ping entries older than this duration
  -l, --max-level int            maximum hop level (0 = unlimited)
  -g, --online                   only ping visors marked online in UT
  -O, --output string            output base filename (writes .json file)
      --recheck-age duration     re-ping entries older than this in continuous mode (default 24h0m0s)
      --remake-remote-tp         remake transport on remote side after failure (retry once)
      --remake-tp                remake local transport after removing failed one (retry once)
      --remove-remote-tp         request remote visor to remove transport if route ping fails
      --remove-tp                remove local transport if route ping fails
  -R, --resume                   resume from output file if it exists
      --retries int              retry attempts if ping fails (default 1)
      --setup-timeout duration   timeout for route setup phase (default 30s)
  -s, --size int                 packet size in KB (default 2)
      --testenv                  use test-deployment service URLs (override SKYWIRETEST)
  -o, --timeout duration         timeout per ping attempt (default 30s)
      --tpdurl string            transport discovery URL (default "http://tpd.skywire.skycoin.com")
      --tps                      verify/update transports via TPS (default: true) (default true)
  -t, --tries int                ping attempts per transport (default 1)
      --uturl string             uptime tracker URL (default "http://ut.skywire.skycoin.com")
  -v, --version string           filter by minimum version
```

## Global Flags

```
  -h, --help         show help menu
      --json         print output as JSON
      --rpc string   RPC server address (env: SKYWIRE_RPC) (default "localhost:3435")
```

---
_Generated by `skywire doc` — do not edit by hand._
