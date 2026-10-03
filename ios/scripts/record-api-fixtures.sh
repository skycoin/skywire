#!/usr/bin/env bash
# Records what a real lite core answers on every visor API route the iOS app
# uses (playbook appendix B, the M2 routes) into CoreClient's test fixtures,
# ios/Packages/CoreClient/Tests/Fixtures/routes/<name>.json.
#
# It runs a throwaway core on the Mac: `make build-mobile`'s binary (the same
# Go code and tags as the phone's core), a config generated with the phone's
# `config gen` argv in a temporary directory, a fresh identity, the local API
# on 127.0.0.1:8000. The routes are then called with curl in the order a
# first launch reaches them, each exchange saved as
# {method, path, status, headers (content-type, set-cookie), body}.
#
# Before anything is written, the machine is scrubbed out of the bodies: its
# public and LAN addresses and host name (read from the summary it just
# recorded) become documentation values, the temporary directory becomes
# /data/skywire, and the service-discovery list is cut to its first five
# entries. The identity is throwaway and deleted with the directory.
#
# Needs: build/skywire-mobile (make build-mobile), jq, python3, curl, network
# (dmsg and service discovery). Port 8000 must be free: stop the Simulator
# app's core and any desktop visor first.
#
# Usage: ios/scripts/record-api-fixtures.sh [path/to/skywire-mobile]
set -euo pipefail

ROOT=$(cd "$(dirname "$0")/../.." && pwd)
BIN=${1:-$ROOT/build/skywire-mobile}
OUT=$ROOT/ios/Packages/CoreClient/Tests/Fixtures/routes
BASE=http://127.0.0.1:8000
PASSWORD='Rec0rd!ng-Pass'

[[ -x $BIN ]] || { echo "no core at $BIN: run make build-mobile" >&2; exit 1; }
if curl -s -m 2 "$BASE/api/ping" >/dev/null; then
  echo "something already answers on $BASE: stop it first" >&2
  exit 1
fi

WORK=$(mktemp -d "${TMPDIR:-/tmp}/skywire-fixtures.XXXXXX")
JAR=$WORK/cookies
VISOR_PID=
cleanup() {
  [[ -n $VISOR_PID ]] && kill "$VISOR_PID" 2>/dev/null && wait "$VISOR_PID" 2>/dev/null
  rm -rf "${WORK:?}"
}
trap cleanup EXIT

# The phone's argv (android ConfigManager.genArgs, mobilecore.PhoneGenOptions),
# then the structural part of the phone profile, enough for a core that runs
# like the phone's. skychat autostarts, as the profile pins it.
"$BIN" config gen -r -o "$WORK/skywire-config.json" -w -i --auth --hvaddr 127.0.0.1:8000 \
  --autoconn --servechat=false --serveproxy=false --servevpn=false \
  --disableapps skysocks,vpn-server,vpn-router,skydex-market,skycoin-web \
  --binpath "$WORK/bin" --nofetch >/dev/null 2>&1
mkdir -p "$WORK/local" "$WORK/bin"
jq --arg d "$WORK" '
  .cli_addr = "" | del(.pty) | del(."skywire-tcp") | .dmsgscp = {disabled: true}
  | .memory_limit = "40MiB" | .local_path = ($d + "/local") | .dmsg.local_relay = {enabled: false}
  | .transport.log_store = {location: ($d + "/local/transport_logs")}
  | .hypervisor |= (del(.lan_dmsg_server) | .db_path = ($d + "/users.db") | .tp_viz = {enable: false})
  | .launcher.bin_path = ($d + "/bin")
  | .launcher.apps |= map(if .name == "skychat" then .auto_start = true else . end)
' "$WORK/skywire-config.json" > "$WORK/config.tmp" && mv "$WORK/config.tmp" "$WORK/skywire-config.json"

(cd "$WORK" && exec "$BIN" visor -c skywire-config.json > visor.log 2>&1) &
VISOR_PID=$!
for _ in $(seq 1 60); do curl -s -m 2 "$BASE/api/ping" >/dev/null && break; sleep 1; done
# Time for dmsg sessions, the STUN probe and skychat to come up.
sleep 20

RAW=$WORK/raw
mkdir -p "$RAW"

# rec <name> <method> <path> [body] [flags: nocookie, csrf]
rec() {
  local name=$1 method=$2 path=$3 body=${4:-} flags=${5:-}
  local args=(-s -m 60 -X "$method" -D "$RAW/.h" -o "$RAW/.b" -w '%{http_code}')
  [[ $flags != *nocookie* ]] && args+=(-b "$JAR" -c "$JAR")
  if [[ $flags == *csrf* ]]; then
    args+=(-H "X-CSRF-Token: $(curl -s "$BASE/api/csrf" | jq -r .csrf_token)")
  fi
  [[ -n $body ]] && args+=(-H 'Content-Type: application/json' --data "$body")
  local status
  status=$(curl "${args[@]}" "$BASE$path")
  python3 - "$RAW/$name.json" "$method" "$path" "$status" "$RAW/.h" "$RAW/.b" <<'PY'
import json, sys
out, method, path, status, hfile, bfile = sys.argv[1:]
headers = {}
for line in open(hfile, encoding="latin-1").read().splitlines()[1:]:
    if ":" in line:
        k, v = line.split(":", 1)
        k = k.strip().lower()
        if k in ("content-type", "set-cookie"):
            headers[k] = headers[k] + ", " + v.strip() if k in headers else v.strip()
body = open(bfile, "rb").read().decode("utf-8")
json.dump({"method": method, "path": path, "status": int(status), "headers": headers, "body": body},
          open(out, "w"), indent=2, sort_keys=True)
PY
  echo "$status $method $path -> $name"
}

rec ping GET /api/ping "" nocookie
rec csrf GET /api/csrf "" nocookie
rec user-unauthorized GET /api/user "" nocookie
rec user-exists-false GET /api/user-exists "" nocookie
rec create-account POST /api/create-account "{\"username\":\"admin\",\"password\":\"$PASSWORD\"}"
rec user-exists-true GET /api/user-exists "" nocookie
rec login-wrong-password POST /api/login '{"username":"admin","password":"Wr0ng!pass"}' nocookie
rec login POST /api/login "{\"username\":\"admin\",\"password\":\"$PASSWORD\"}"
rec login-already POST /api/login "{\"username\":\"admin\",\"password\":\"$PASSWORD\"}"
rec user GET /api/user
rec about GET /api/about
PK=$(curl -s -b "$JAR" "$BASE/api/about" | jq -r .public_key)
rec about-unauthorized GET /api/about "" nocookie
rec summary GET "/api/visors/$PK/summary"
rec service-health GET /api/service-health
rec runtime-logs GET "/api/visors/$PK/runtime-logs?since=0"
LATEST=$(curl -s -b "$JAR" "$BASE/api/visors/$PK/runtime-logs?since=0" | jq .latest)
rec runtime-logs-caught-up GET "/api/visors/$PK/runtime-logs?since=$((LATEST + 1000000))"
rec visors-summary GET /api/visors-summary
rec app-skysocks-client GET "/api/visors/$PK/apps/skysocks-client"
rec app-connections-not-running GET "/api/visors/$PK/apps/skysocks-client/connections"
rec app-stats-not-running GET "/api/visors/$PK/apps/skysocks-client/stats"
rec app-connections-skychat GET "/api/visors/$PK/apps/skychat/connections"
rec app-stats-skychat GET "/api/visors/$PK/apps/skychat/stats"
rec app-logs-not-running GET "/api/visors/$PK/apps/skysocks-client/logs"
rec app-logs-skychat GET "/api/visors/$PK/apps/skychat/logs"
SINCE=$(curl -s -b "$JAR" "$BASE/api/visors/$PK/apps/skychat/logs" | jq -r .last_log_timestamp)
rec app-logs-no-new GET "/api/visors/$PK/apps/skychat/logs?since=$(jq -rn --arg s "$SINCE" '$s|@uri')"
rec put-app-without-csrf PUT "/api/visors/$PK/apps/skysocks-client" '{"args":"--addr 127.0.0.1:1080 --reconnect"}'
rec put-app PUT "/api/visors/$PK/apps/skysocks-client" '{"args":"--addr 127.0.0.1:1080 --reconnect"}' csrf
rec router-settings GET "/api/visors/$PK/router-settings"
# The four fields CoreClient writes back, never the whole document: the GET's
# knobs would be pinned into the config (CoreClient.updateRouterSettings).
SETTINGS=$(curl -s -b "$JAR" "$BASE/api/visors/$PK/router-settings" |
  jq -c '{force_local_routes, existing_tp_only, min_hops: 2, transport_preference}')
rec put-router-settings PUT "/api/visors/$PK/router-settings" "$SETTINGS" csrf
rec svc-fetch-proxy GET "/api/svc-fetch?service=sd&path=$(jq -rn '"/api/services?type=proxy"|@uri')"
rec dmsg-reconnect POST /api/dmsg/reconnect '{}' csrf
rec restart POST "/api/visors/$PK/restart" '{}' csrf
rm -f "$RAW/.h" "$RAW/.b"

# Scrub the machine out, then replace the fixture set.
python3 - "$RAW" "$WORK" <<'PY'
import glob, json, os, sys
raw, work = sys.argv[1:]
overview = json.loads(json.load(open(os.path.join(raw, "summary.json")))["body"])["overview"]
subs = [(os.path.realpath(work), "/data/skywire"), (work, "/data/skywire")]
for field, placeholder in (("public_ip", "203.0.113.10"), ("local_ip", "100.64.0.10"), ("hostname", "host.example.ts.net")):
    value = overview.get(field) or ""
    if len(value) > 3:
        subs.append((value, placeholder))
for path in glob.glob(os.path.join(raw, "*.json")):
    fixture = json.load(open(path))
    body = fixture["body"]
    for old, new in subs:
        body = body.replace(old, new)
    if os.path.basename(path) == "svc-fetch-proxy.json":
        body = json.dumps(json.loads(body)[:5]) + "\n"
    fixture["body"] = body
    with open(path, "w") as f:
        json.dump(fixture, f, indent=2, sort_keys=True)
        f.write("\n")
PY
mkdir -p "$OUT"
find "$OUT" -name '*.json' -delete
cp "$RAW"/*.json "$OUT"/
echo "recorded $(find "$OUT" -name '*.json' | wc -l | tr -d ' ') exchanges into $OUT (pk $PK, throwaway)"
