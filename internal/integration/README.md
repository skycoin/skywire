# E2E Tests

This directory contains end-to-end integration tests for Skywire.

## Quick Start

### First-Time Setup

**One-time system configuration (requires sudo):**
```bash
sudo ./ci_scripts/setup-sudo-requirements.sh
```

This configures:
- IP aliases (12.12.12.1-255) on loopback interface for service discovery
- IP forwarding for routing tests

These settings persist across reboots, so you only need to run this once per machine.

### Running Tests

```bash
# Build test environment
make e2e-build

# Start containers
make e2e-run

# Run tests
make e2e-test

# Wire transports between all three visors and print the skychat UIs + PKs,
# for driving the chat app from a browser (see "Manual Browser Testing" below)
make e2e-skychat

# Stop containers (keeps state)
make e2e-stop

# Clean everything
make e2e-clean
```

## Manual Browser Testing (skychat)

Every visor runs skychat as an in-process app on container port 8001, and
docker-compose publishes the three UIs on the host — so the chat app can be
driven from a real browser against three real visors (DMs, groups, file
transfers, voice/video messages).

With the environment up (`make e2e-build && make e2e-run`):

```bash
make e2e-skychat
```

That pairs every visor with every other one and prints each visor's URL
alongside its public key:

| visor   | skychat UI            | also                             |
|---------|-----------------------|----------------------------------|
| visor-a | http://127.0.0.1:8001 |                                  |
| visor-b | http://127.0.0.1:8002 | hypervisor: http://127.0.0.1:8000 |
| visor-c | http://127.0.0.1:8003 |                                  |

Open two of them in separate browser windows and paste the other visor's
public key in to start a DM.

**Why it adds transports.** The composer's network select defaults to `skynet`,
which routes over a transport. `dmsg` needs none — it goes through the dmsg
server — so switching that select is the fallback if a transport won't come up.

**Why 127.0.0.1 and not a LAN address.** `getUserMedia` (microphone/camera) and
the Notifications API only work in a secure context, which plain HTTP satisfies
for `127.0.0.1`/`localhost` but *not* for a LAN IP. Reaching these UIs over the
network silently disables voice/video messages and desktop notifications.

**After editing the UI.** `cmd/apps/skychat/commands/static/index.html` is
`go:embed`-ed into the visor binary and skychat runs in-process, so a browser
reload won't pick up changes — rebuild the image and let compose recreate the
containers:

```bash
make e2e-dock && make e2e-run   # e2e-dock is the image build without the sudo step
```

**Not exercisable here: real-time voice calls.** The visor captures and plays
host audio (`pkg/skychat/call`), and these alpine containers run no
PulseAudio/PipeWire daemon, so the call manager reports disabled and the UI
hides its call controls. Voice/video *messages* are unaffected — the browser
records them and they ship over the ordinary file-transfer path.

**If you regenerate the visor configs**, check skychat's args still read
`--addr *:8001 --pair-enable`. `make e2e-config` produces them itself —
`SKYCHATADDR='*:8001'` in `docker/integration/e2e.conf` supplies the
all-interfaces bind, and pairing is on by default — but they are what both the
test suite (`http://visor-a:8001`) and the published host ports depend on, and
what enables groups and the `/voice` endpoints, so it is worth a look.

## Test Structure

The e2e tests run in Docker containers with the following architecture:

- **3 visor nodes** (visor-a, visor-b, visor-c) - Main test nodes. visor-b is
  also the hypervisor for the other two.
- **2 service containers** - `deployment-services` runs the nine deployment-side
  services (transport-discovery, route-finder, dmsg-discovery, dmsg-server,
  setup-node, service-discovery, address-resolver, transport-setup, stun-server)
  as goroutines in one `skywire svc run` process, keeping each service's
  hostname as a network alias; `redis` backs them all, one logical DB per
  service. The standalone uptime-tracker is gone — uptime is integrated into the
  discovery services.
- **1 test runner** - `e2e-test` (compose profile `test`, started by
  `make e2e-test`) executes the Go suite from inside the networks.
- **Custom networks** - Isolated Docker networks for controlled testing

### Test Categories

1. **Basic Tests** - Container setup, CLI functionality, app listing
2. **Transport Tests** - Adding/removing transports, transport types
3. **Messaging Tests** - Skychat messaging, multi-hop routing
4. **Restart Tests** - Visor restarts, transport persistence
5. **VPN Tests** - VPN client/server, killswitch, transport types

## Debugging

### View Container Logs

```bash
# All containers
make e2e-logs

# Specific container
docker logs visor-a
docker logs visor-b
docker logs visor-c
```

### Save Logs to Files

```bash
docker logs visor-a > visor-a.log 2>&1
docker logs visor-b > visor-b.log 2>&1
docker logs visor-c > visor-c.log 2>&1
```

### Execute Commands in Containers

```bash
# CLI commands
docker exec visor-a /release/skywire cli visor --rpc visor-a:3435 app ls

# Shell access
docker exec -it visor-a /bin/sh
```

### Common Issues

**"System requirements not configured"**
- Run: `sudo ./ci_scripts/setup-sudo-requirements.sh`

**Port conflicts (8000-8003, etc.)**
- Four ports are published, all bound to `127.0.0.1`: the skychat UIs on
  8001/8002/8003 (visor-a/b/c) and the hypervisor UI on 8000. If one is already
  taken on your machine, that container fails to start with a bind error —
  change the host side of the mapping in `docker/docker-compose.yml`.
- Nothing else is published. The tests reach services by container name
  (`http://visor-a:8001`), so publishing exists only for manual browser use.

**Tests fail with "EOF" or transport errors**
- Transports may be in stale state from previous runs
- Solution: `make e2e-clean && make e2e-build && make e2e-run`

**Build failures**
- Check Docker daemon is running
- Ensure REGISTRY env var is set: `export REGISTRY=skycoin`

## CI Environment

In GitHub Actions, the workflow:
1. Runs `create-ip-aliases.sh` with sudo (handled by CI runner)
2. Builds: `make e2e-build`
3. Starts: `make e2e-run`  
4. Tests: `make e2e-test`
5. Stops: `make e2e-stop`

The CI runner has sudo access, so no manual setup is needed there.

## Architecture Details

### Networks

- `docker_srv` (175.0.0.0/16) - Service network
- `docker_visors` (173.0.0.0/16) - Visor interconnection
- `docker_intra` (174.0.0.0/16) - Internal container communication

### Build Tags

All containers are tagged with `e2e` tag for isolation from production builds.

### Test Timeout

Default timeout: 15 minutes (configurable in Makefile)

## Contributing

When adding new tests:

1. Follow existing test patterns in `internal/integration/`
2. Use helper functions from `env_test.go` and `util_test.go`
3. Add proper cleanup in test teardown
4. Document complex test scenarios
5. Ensure tests are idempotent (can run multiple times)

## Troubleshooting Test Failures

### SendSkyMessage / Messaging Tests

**Symptoms**: EOF errors, "no suitable transport"
**Causes**: 
- Transport not fully established
- Stale transport entries in TPD
- Race condition during app startup

**Solution**: These should now be fixed by transport duplicate key handling

### VPN Tests

**Symptoms**: App shows "errored" status
**Causes**:
- Previous app error persisting across restarts
- VPN client can't reach VPN server
- Transport issues

**Solution**: Error state now cleared before app start

### Restart Tests

**Symptoms**: Timeouts, connection failures after restart
**Causes**:
- Containers not fully ready after restart
- Transport re-establishment delay
- Service discovery lag

**Debug**: Check visor logs for transport establishment and route setup
