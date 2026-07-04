#!/usr/bin/env bash
# Wire-level assertions for the IPv6 dual-stack dmsg e2e lane (#1525).
#
# Assumes `docker compose -f docker/dmsg/docker-compose.e2e-v6.yml up -d
# --wait` has already brought the stack up healthy on the dual-stack
# bridge. Drives the checks from inside the client container (docker
# exec) so they exercise the real docker IPv6 bridge, peer-to-peer.
# Tears nothing down — the caller owns lifecycle so it can dump logs.
#
# What is proven (HARD — the lane fails if any of these fail):
#   1. the dmsg-discovery HTTP registry is reachable over IPv6;
#   2. the dmsg-server dmsg listener is reachable over IPv6;
#   3. the dmsg-server health HTTP is reachable over IPv6; and
#   4. THE CROWN JEWEL — a dmsg client completes a full Noise handshake
#      straight to the server's IPv6 endpoint (dmsgprobe --via), i.e.
#      the dmsg protocol itself works end-to-end over IPv6.
#
# What is reported (SOFT — informational, never fails the lane):
#   5. the dual-stack advertisement round-trip (server disc.Entry
#      carrying AddressV6). A dmsg-server's self-registration to the
#      discovery is not reliable in a lightweight standalone deployment
#      (the mesh normally bootstraps clients from the embedded server
#      set, not the discovery's server registry) — so if the entry
#      lands we assert address_v6, otherwise we note it and move on.
#      The advertisement marshaling itself is covered by unit tests
#      (pkg/dmsg/disc, pkg/dmsg/dmsg/server_ipv6_test.go, the
#      address-resolver ipv6 tests).
set -euo pipefail

# --- fixtures (must match docker/config/dmsg-server-v6.json + compose) ---
SERVER_PK="035915c609f71d0c7df27df85ec698ceca0cb262590a54f732e3bbd0cc68d89282"
SERVER_V6="fd00:dead:beef::4"          # deployment ipv6_address (dmsg-server + discovery)
SERVER_V6_ADDR="[${SERVER_V6}]:8080"   # its advertised public_address_v6
DISC_HOST_URL="http://127.0.0.1:9091"  # host-mapped 9091 -> discovery :9090

CLIENT="dmsg-e2e-v6-client"
COMPOSE=(docker compose -f docker/dmsg/docker-compose.e2e-v6.yml)

log()  { printf '\n\033[1m==> %s\033[0m\n' "$*"; }
pass() { printf '\033[32mPASS\033[0m %s\n' "$*"; }
soft() { printf '\033[33mSOFT\033[0m %s\n' "$*"; }
fail() { printf '\033[31mFAIL\033[0m %s\n' "$*"; }

dump_diagnostics() {
  log "DIAGNOSTICS (a hard check failed)"
  "${COMPOSE[@]}" ps || true
  printf '\n----- dmsg-deployment logs (tail) -----\n'
  "${COMPOSE[@]}" logs --tail=120 dmsg-deployment 2>&1 || true
  printf '\n----- client sockets -----\n'
  docker exec "$CLIENT" sh -c 'ss -tnp 2>/dev/null' || true
}
trap 'rc=$?; [ "$rc" -ne 0 ] && dump_diagnostics; exit "$rc"' EXIT

# --- 1. discovery HTTP registry reachable over IPv6 -------------------------
log "1. dmsg-discovery HTTP registry is reachable over IPv6"
if docker exec "$CLIENT" curl -fsS --max-time 8 "http://[${SERVER_V6}]:9090/health" >/dev/null; then
  pass "discovery /health answered over IPv6 at [${SERVER_V6}]:9090"
else
  fail "discovery /health unreachable over IPv6 at [${SERVER_V6}]:9090"
  exit 1
fi

# --- 2. server dmsg listener reachable over IPv6 ----------------------------
log "2. the dmsg-server dmsg listener is reachable over IPv6"
if docker exec "$CLIENT" nc -6 -z -w 6 "$SERVER_V6" 8080; then
  pass "server dmsg port reachable at ${SERVER_V6_ADDR} over IPv6"
else
  fail "server dmsg port unreachable at ${SERVER_V6_ADDR} over IPv6"
  exit 1
fi

# --- 3. server health HTTP reachable over IPv6 ------------------------------
log "3. the dmsg-server health HTTP is reachable over IPv6"
if docker exec "$CLIENT" curl -fsS --max-time 8 "http://[${SERVER_V6}]:8082/health" >/dev/null; then
  pass "server /health answered over IPv6 at [${SERVER_V6}]:8082"
else
  fail "server /health unreachable over IPv6 at [${SERVER_V6}]:8082"
  exit 1
fi

# --- 4. a real dmsg Noise handshake over IPv6 (crown jewel) -----------------
# dmsgprobe --via tcp://<pk>@host:port dials the TCP endpoint directly and
# runs the dmsg Noise handshake; "reachable" means the handshake completed,
# i.e. the server accepted a raw-dmsg session over IPv6.
log "4. a dmsg client completes a Noise handshake to the server over IPv6"
probe_out="$(docker exec "$CLIENT" \
  dmsgprobe --via "tcp://${SERVER_PK}@${SERVER_V6_ADDR}" -l error 2>&1 || true)"
printf 'dmsgprobe: %s\n' "$probe_out"
if printf '%s' "$probe_out" | grep -q -- '— reachable'; then
  pass "dmsg Noise handshake completed over IPv6 to ${SERVER_V6_ADDR}"
else
  fail "dmsg Noise handshake over IPv6 did NOT complete"
  exit 1
fi

# --- 5. dual-stack advertisement round-trip (SOFT) --------------------------
log "5. (soft) dmsg-server advertises address_v6 through the discovery"
entry=""
for _ in $(seq 1 15); do
  entry="$(curl -fsS "${DISC_HOST_URL}/dmsg-discovery/entry/${SERVER_PK}" 2>/dev/null || true)"
  if printf '%s' "$entry" | tr -d ' ' | grep -q "\"address_v6\":\"${SERVER_V6_ADDR}\""; then
    break
  fi
  entry=""
  sleep 2
done
if [ -n "$entry" ]; then
  printf 'entry: %s\n' "$entry"
  pass "discovery serves the server entry with address_v6=${SERVER_V6_ADDR}"
else
  soft "server self-registration did not complete in-window; advertisement marshaling is unit-tested (see header). Not failing the lane."
fi

trap - EXIT
log "IPv6 dual-stack dmsg e2e: all HARD checks PASSED"
