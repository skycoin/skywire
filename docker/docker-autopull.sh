#!/bin/bash
# Pull skycoin/skywire:test and recreate containers if the image changed.
# Verifies the new binary works before deploying.
# Configure via /etc/skywire.conf or environment:
#   DOCKER_IMAGE    - image to pull (default: skycoin/skywire:test)
#   COMPOSE_DIR     - directory containing docker-compose.yml
set -e

# Source configuration
SKYENV=${SKYENV:-/etc/skywire.conf}
[[ -f "$SKYENV" ]] && source "$SKYENV"

IMAGE="${DOCKER_IMAGE:-skycoin/skywire:test}"
COMPOSE_DIR="${COMPOSE_DIR:-$(cd "$(dirname "$0")" && pwd)}"

OLD_ID=$(docker image inspect "$IMAGE" --format '{{.Id}}' 2>/dev/null || echo "none")
docker pull "$IMAGE" -q
NEW_ID=$(docker image inspect "$IMAGE" --format '{{.Id}}' 2>/dev/null || echo "")

if [ "$OLD_ID" = "$NEW_ID" ]; then
    exit 0
fi

# Smoke test: verify the binary in the new image runs
if ! docker run --rm "$IMAGE" --help >/dev/null 2>&1; then
    echo "$(date): New image failed smoke test, skipping update"
    exit 1
fi

echo "$(date): Image updated, recreating containers"
cd "$COMPOSE_DIR"
docker compose up -d --force-recreate
