#!/usr/bin/env bash
# scripts/wasm-headless/run.sh — drive the Tier B headless wasm-visor smoke:
# build the std-Go wasm blob (if absent or STALE=1), start the loopback dmsg
# server helper, run the Node harness against it, reap everything. Exit code =
# harness verdict. Requires Node ≥22 (global WebSocket).
set -euo pipefail
cd "$(dirname "$0")/../.."

WASM=build/wasm-visor-go/wasm-visor.wasm
EXECJS=build/wasm-visor-go/wasm_exec.js
if [[ ! -f $WASM || ! -f $EXECJS || ${STALE:-0} = 1 ]]; then
  echo "== building wasm-visor (std Go) =="
  make wasm-visor
fi

NODE_MAJOR=$(node -p 'process.versions.node.split(".")[0]')
if (( NODE_MAJOR < 22 )); then
  echo "node >= 22 required (global WebSocket); have $(node -v)" >&2
  exit 2
fi

echo "== starting loopback dmsg server =="
# Keep the helper's stdin open via a fifo; closing it (cleanup) stops the server.
FIFO=$(mktemp -u)
mkfifo "$FIFO"
OUT=$(mktemp)
ERR=$(mktemp)
go run ./scripts/wasm-headless/localdmsg <"$FIFO" >"$OUT" 2>"$ERR" &
SRV=$!
exec 9>"$FIFO" # hold the write end open
cleanup() { exec 9>&- || true; rm -f "$FIFO"; kill "$SRV" 2>/dev/null || true; }
trap cleanup EXIT

for _ in $(seq 1 100); do
  grep -q '^READY$' "$OUT" 2>/dev/null && break
  sleep 0.2
done
grep -q '^READY$' "$OUT" || { echo "localdmsg never became ready:"; cat "$OUT" "$ERR"; exit 1; }
SEEDPK=$(grep '^SEEDPK=' "$OUT" | cut -d= -f2)
SEEDWS=$(grep '^SEEDWS=' "$OUT" | cut -d= -f2-)
echo "seed: $SEEDPK @ $SEEDWS"

echo "== running Node harness =="
# The smoke exercises a real dmsg session, so transient reachability blips can
# fail a single run. Retry once against the same (still-running) local seed
# before declaring failure; the harness itself now fails fast with a diagnostic
# rather than hanging, so a retry is cheap.
ATTEMPTS=2
for i in $(seq 1 "$ATTEMPTS"); do
  if node scripts/wasm-headless/harness.mjs "$WASM" "$EXECJS" "$SEEDPK" "$SEEDWS"; then
    exit 0
  fi
  if (( i < ATTEMPTS )); then
    echo "== harness attempt $i failed; retrying (dmsg reachability can flake) =="
    sleep 2
  fi
done
echo "== harness failed after $ATTEMPTS attempts =="
exit 1
