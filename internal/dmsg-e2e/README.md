# DMSG E2E Tests

This directory contains end-to-end tests for DMSG client utilities (`dmsg curl` and `dmsg web`).

## Overview

The e2e tests verify that:
1. DMSG discovery and server services start correctly
2. `dmsg curl` can fetch content over DMSG protocol
3. `dmsg web srv` can serve HTTP over DMSG
4. `dmsg web` proxy works correctly
5. The version field bug (fixed in recent commits) doesn't regress

## Architecture

The tests use Docker Compose to create a local DMSG deployment with:
- **redis**: Redis instance for discovery service
- **dmsg-discovery**: DMSG discovery service running in test mode
- **dmsg-server**: DMSG server for routing traffic
- **dmsg-client**: Test client container with dmsg utilities

## Running Tests

### Prerequisites
- Docker and Docker Compose installed
- Go 1.25 or later

### Quick Start

```bash
# From the dmsg root directory
./scripts/run-e2e-tests.sh
```

Or run manually:

```bash
# Build and start services
cd docker
docker-compose -f docker-compose.e2e.yml up -d

# Wait for services to be ready
sleep 15

# Run tests
go test -v -tags !no_ci ./internal/e2e/...

# Clean up
docker-compose -f docker-compose.e2e.yml down -v
```

### Using Make

```bash
make test-e2e
```

## Test Cases

### TestDiscoveryIsRunning
Verifies that the DMSG discovery service container is running.

### TestDmsgServerIsRunning
Verifies that the DMSG server container is running.

### TestDmsgCurlBasic
Tests basic `dmsg curl` functionality:
1. Starts an HTTP server using `dmsg web srv`
2. Uses `dmsg curl` to fetch content from it
3. Verifies the response is received

### TestDmsgWebProxy
Tests the `dmsg web` SOCKS5 proxy:
1. Starts `dmsg web` with proxy and web interface
2. Verifies the services are listening on expected ports

### TestVersionFieldPresent (Regression Test)
**Critical test** that would have caught the recent version field bug:
- Tests that `dmsg curl -Z` (HTTP discovery mode) works correctly
- Before the fix, this would fail with "entry validation error: entry has no version"
- Verifies all Entry structs include the required version field

### TestDmsgCurlToDiscovery
Tests querying the discovery service:
1. Uses `dmsg curl` to fetch available servers from discovery
2. Verifies our test dmsg server is listed

## Configuration

Test configuration is in `docker/e2e/`:
- `dmsg-server.json`: DMSG server configuration with fixed keys for testing

## Troubleshooting

### Services not starting
Check container logs:
```bash
docker-compose -f docker/docker-compose.e2e.yml logs
```

### Tests timing out
Increase wait time in test or script:
```bash
sleep 30  # Instead of 15
```

### Port conflicts
The e2e environment uses ports:
- 6380: Redis
- 9090: DMSG Discovery
- 8080: DMSG Server

Ensure these ports are available before running tests.

## Adding New Tests

1. Add test function to `internal/e2e/e2e_test.go`
2. Use the `TestEnv` helper to interact with containers
3. Test should focus on dmsg client utilities functionality
4. Include assertions for expected behavior
5. Run locally to verify before committing

## CI Integration

To run in CI, set the `!no_ci` build tag and ensure Docker is available:

```yaml
- name: Run E2E Tests
  run: |
    cd dmsg
    ./scripts/run-e2e-tests.sh
```
