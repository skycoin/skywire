# E2E Tests

This directory contains end-to-end integration tests for Skywire.

## Quick Start

### First-Time Setup

**One-time system configuration (requires sudo):**
```bash
sudo ./ci_scripts/setup-sudo-requirements.sh
```
OR

```bash
make set-forwarding
```

This configures:
- IP aliases (12.12.12.1-255) on loopback interface for service discovery
- IP forwarding for routing tests

These settings persist across reboots, so you only need to run this once per machine.

### Running Tests

```bash
# Build test environment
make e2e-dock

# Start containers
make e2e-run

# Run tests
make e2e-test

# Stop containers (keeps state)
make e2e-stop

# Clean everything
make e2e-clean
```

## Test Structure

The e2e tests run in Docker containers with the following architecture:

- **3 visor nodes** (visor-a, visor-b, visor-c) - Main test nodes
- **13 service containers** - Dmsg servers, transport discovery, route finder, etc.
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

**Port conflicts (5432, 8000, 8001, etc.)**
- The docker-compose.yml has these ports commented out by default
- Tests access services via container names, not host ports

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
