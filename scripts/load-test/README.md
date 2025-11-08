# Skywire Load Test Script

A bash script for load testing and development purposes. Creates multiple Skywire visor containers to simulate high-load scenarios and test system performance.

## Purpose

This script is used internally for:
- Load testing infrastructure
- Testing system limits and resource usage
- Development and debugging under high-load conditions
- Simulating multiple visor instances

Each container generates its own unique configuration automatically.

## Prerequisites

- Linux (Ubuntu/Debian/RedHat/CentOS/Fedora) or macOS
- Bash shell
- sudo privileges (for Docker installation if needed)

**Note**: Docker will be automatically installed if not present.

## Setup

The script is located in `scripts/` directory. Make it executable:

```bash
chmod +x scripts/load-test.sh
```

## Usage

### Start Load Test

Start a specified number of containers with a delay between each:

```bash
./scripts/load-test.sh start <number_of_containers> <delay_seconds>
```

**Examples:**
```bash
# Start 200 containers with 10 seconds delay between each
./scripts/load-test.sh start 200 10

# Start 50 containers with 5 seconds delay
./scripts/load-test.sh start 50 5
```

### Monitor Containers

**Check status:**
```bash
./scripts/load-test.sh status
```

**Get quick count:**
```bash
./scripts/load-test.sh count
```

**View resource usage:**
```bash
./scripts/load-test.sh stats
```

**Get aggregated summary:**
```bash
./scripts/load-test.sh summary
```

Example output:
```
=== Skywire Containers Summary Report ===

Containers:
  Running: 200
  Stopped: 0
  Total: 200

=== Aggregated Resources ===
  Total CPU: 45.30%
  Total Memory: 5.86 GiB
  Avg Memory/Container: 30.00 MiB
```

### View Logs

```bash
./scripts/load-test.sh logs <container_number>
```

### Cleanup

**Stop all containers:**
```bash
./scripts/load-test.sh stop
```

**Stop and remove all containers:**
```bash
./scripts/load-test.sh clean
```

## Commands

| Command | Description |
|---------|-------------|
| `start <num> <delay>` | Start specified number of containers |
| `stop` | Stop all running containers |
| `clean` | Stop and remove all containers |
| `restart` | Restart all containers |
| `status` | Show status of all containers |
| `stats` | Show resource usage |
| `logs <N>` | Follow logs of container N |
| `count` | Show running/total count |
| `summary` | Show aggregated resource report |

## How It Works

1. **Docker Check**: The script first checks if Docker is installed and offers to install it automatically if not present.

2. **Container Creation**: Each container is created with:
   - A unique name (e.g., `skywire-visor-1`, `skywire-visor-2`, etc.)
   - Auto-restart policy for high availability
   - Its own configuration generated at startup

3. **Configuration Generation**: Inside each container, the following happens:
   ```bash
   /release/skywire cli config gen -t -o /tmp/config.json
   /release/skywire visor -c /tmp/config.json
   ```
   This ensures each instance has a unique configuration without manual intervention.

4. **Resource Management**: The script provides various tools to monitor and manage resource usage across all instances.

## Configuration

You can modify these variables at the top of the script:

```bash
IMAGE_NAME="skycoin/skywire:test"    # Docker image to use
CONTAINER_PREFIX="skywire-visor"     # Prefix for container names
```

## Resource Requirements

- **Per Container**: ~30MB RAM
- **200 Containers**: ~6GB RAM total
- **CPU**: Varies based on network activity

## Troubleshooting

### Docker not found
The script will automatically offer to install Docker.

### Permission denied
Add your user to docker group:
```bash
sudo usermod -aG docker $USER
newgrp docker
```

### Container fails to start
Check the logs:
```bash
./scripts/load-test.sh logs <container_number>
```
