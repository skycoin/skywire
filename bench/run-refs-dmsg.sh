#!/bin/sh
# run-refs-dmsg.sh — the `ref-dmsg-only` reference set: a SOCKS5 proxy carried by
# PURE DMSG. No skywire route, no route group, no transport, no visor app on
# either end — two standalone dmsg clients, each with its own throwaway key,
# meeting on one dmsg server.
#
#   bench/run-refs-dmsg.sh <exit pk> <out dir> [trials] [sink] [srv pk@ip:port]
#
# This is the bar for relay-leg viability (memory: mux follow-up 2): everything
# the routed sets measure is the router's cost ON TOP of what this row costs.
#
# Shape (identical columns to bench.sh, one TSV, header line first):
#   ref-dmsg-only.tsv        <trials> x {10,50} MB x {down,up}, hash-verified
#   ref-dmsg-only.meta       the two keys, the pinned dmsg server, the servers
#                            each side actually sat on, exit RSS before/after
#
# ------------------------------------------------------------------ PREREQS --
# Two gaps in `skywire dmsg socks` block this script today; it refuses to run
# until both are closed (see the preflight below and bench/README.md):
#
#  1. cmd/dmsg/dmsg-socks5/commands/dmsg-socks5.go:236-300 — `dmsg socks client`
#     dials ONE dmsg stream (:283) and never uses it; the local SOCKS5 server
#     (:292-300) runs with a zero socks5.Config, so its CONNECTs are dialed by
#     net.Dial from THIS host. The client side does not carry traffic over dmsg
#     at all. Until it pipes each accepted TCP conn through its own
#     dmsgC.DialStream, a row from this script would silently measure clearnet.
#     The preflight cannot see this statically — it is asserted by the operator
#     via DMSG_SOCKS_PIPED=1, which is the acknowledgement that the fix landed.
#
#  2. the same init() never calls dmsgclient.InitFlags, so -S/--srv, -e/--sess,
#     -A/--disc-addr, -B/--direct are absent. Both sides need -S to be pinned to
#     ONE server: `dmsg socks server` runs under StartDmsgSelfHostedDisc, whose
#     PutEntry no-ops (pkg/dmsg/dmsgclient/fallback_disc.go:125-141), so the
#     server publishes NO discovery entry and the client's dial falls through to
#     dialViaConnectedServers over whichever 2 servers it happened to pick
#     (pkg/dmsg/dmsg/client_dial.go:73-97). Unpinned, the two sides meet by luck.
#     The preflight greps --help for --srv.
set -u
CLI=${CLI:-/home/d0mo/go/bin/skywire}
exit_pk=$1; out=$2; trials=${3:-5}; sink=${4:-http://127.0.0.1:18080}; srv=${5:-}
here=$(dirname "$0")
mkdir -p "$out"
local_commit=$(git -C "$here/.." rev-parse --short=9 HEAD)
sizes="10000000 50000000"
socks=127.0.0.1:1081          # never 1080 — that is the visor's routed proxy
dport=1081                    # dmsg port the server listens on
unit=bench-dmsg-socks         # transient unit on the exit; never the visor's
set_name=ref-dmsg-only
f="$out/$set_name.tsv"; meta="$out/$set_name.meta"
clog="$out/$set_name.client.log"

x() { # one-shot command on the exit, dash sh, DEBUG lines dropped
	timeout 90 $CLI cli pty exec "$exit_pk" --timeout 40s -- /bin/sh -c "$1" 2>&1 | grep -v DEBUG
}

# --------------------------------------------------------------- preflight --
$CLI dmsg socks client --help 2>&1 | grep -q -- '--srv' || {
	echo "ABORT: \`dmsg socks client\` has no --srv."
	echo "  add dmsgclient.InitFlags(serveCmd) and dmsgclient.InitFlags(proxyCmd)"
	echo "  to cmd/dmsg/dmsg-socks5/commands/dmsg-socks5.go init()"
	exit 2
}
[ "${DMSG_SOCKS_PIPED:-}" = 1 ] || {
	echo "ABORT: set DMSG_SOCKS_PIPED=1 only once dmsg-socks5.go proxyCmd pipes"
	echo "  each accepted TCP conn through dmsgC.DialStream. Today it does not"
	echo "  (dmsg-socks5.go:283 dials a stream it never uses; :292-300 serves a"
	echo "  plain local socks5 whose default Dial is net.Dial) — a row taken now"
	echo "  would be clearnet, not dmsg."
	exit 2
}

# ------------------------------------------------------- keys + the server --
# Both keys are minted HERE and are throwaway. The exit process gets its own
# key via DMSGSK in its unit environment — it is never the visor's key (a second
# dmsg client on the visor's PK would evict the visor from its own servers).
srvkeys=$($CLI cli config gen-keys --json)
clikeys=$($CLI cli config gen-keys --json)
srv_pk=$(echo "$srvkeys" | jq -r '..|objects|select(has("public_key"))|.public_key' | head -1)
srv_sk=$(echo "$srvkeys" | jq -r '..|objects|select(has("secret_key"))|.secret_key' | head -1)
cli_pk=$(echo "$clikeys" | jq -r '..|objects|select(has("public_key"))|.public_key' | head -1)
cli_sk=$(echo "$clikeys" | jq -r '..|objects|select(has("secret_key"))|.secret_key' | head -1)

# The dmsg server both sides pin. Default: the least-loaded one in discovery.
if [ -z "$srv" ]; then
	srv=$($CLI cli mdisc servers --json 2>/dev/null |
		jq -r '[..|objects|select(has("public_key") and has("address"))]
		       | sort_by(-.avail_sess) | .[0] | "\(.public_key)@\(.address)"')
fi
[ -n "$srv" ] && [ "$srv" != "null@null" ] || { echo "ABORT: no dmsg server to pin"; exit 2; }
echo "pinned dmsg server: $srv"
echo "server pk: $srv_pk   client pk: $cli_pk"

exit_commit=$(timeout 60 $CLI cli visor state --via "dmsg://$exit_pk" --select summary --json 2>/dev/null |
	jq -r '.summary.overview.build_info.commit[0:9] // "unknown"')

# Start the exit side as a TRANSIENT SYSTEMD UNIT, not nohup: it survives the
# pty exec that launched it, it is capped so it can never be the process that
# OOMs this 2-core/4 GB box, and it is addressable by name for the stop.
# MemoryMax=384M is ~8x the expected footprint (see the sizing note at the end).
pidf=/run/$unit.pid
start_exit="
bin=\$(systemctl show -p ExecStart --value skywire | grep -o 'path=[^ ;]*' | cut -d= -f2)
bin=\$(readlink -f \"\$bin\")
systemctl reset-failed $unit 2>/dev/null
systemd-run --unit=$unit --collect -p MemoryMax=384M -p Restart=no -E DMSGSK=$srv_sk \
  \"\$bin\" dmsg socks server -q $dport -w $cli_pk -S $srv >/dev/null 2>&1
sleep 3
p=\$(systemctl show -p MainPID --value $unit)
echo \"\$p\" > $pidf
echo \"unit=\$(systemctl is-active $unit) pid=\$p bin=\$bin\"
"
cli_pid=""
stop_all() {
	[ -n "$cli_pid" ] && kill "$cli_pid" 2>/dev/null
	# The ONLY remote process this script may ever kill: the pid in OUR pid
	# file, and only after its cmdline confirms it is our socks server. The
	# visor is never touched.
	x "p=\$(cat $pidf 2>/dev/null)
if [ -n \"\$p\" ] && tr '\\0' ' ' < /proc/\$p/cmdline 2>/dev/null | grep -q 'dmsg socks server'; then
	kill \$p 2>/dev/null; sleep 2
fi
systemctl stop $unit 2>/dev/null
systemctl reset-failed $unit 2>/dev/null
rm -f $pidf
echo \"stopped: \$(systemctl is-active $unit)\""
}
trap 'stop_all' EXIT INT TERM

echo "-- exit side"
x "$start_exit"

exit_rss() { # RssAnon of OUR process only, by the pid file we wrote
	x "p=\$(cat $pidf 2>/dev/null); [ -n \"\$p\" ] && grep -E '^RssAnon' /proc/\$p/status || echo 'RssAnon: ?'"
}
rss_before=$(exit_rss | tr -s ' ')

# ------------------------------------------------------------- local client --
DMSGSK=$cli_sk $CLI dmsg socks client -k "$srv_pk" -p "${socks#*:}" -q "$dport" -S "$srv" \
	>"$clog" 2>&1 &
cli_pid=$!

# probe until the tunnel answers (dmsg session + stream + socks handshake)
ok=0; i=1
while [ $i -le 30 ]; do
	code=$(curl -s -m 30 --socks5-hostname "$socks" -o /dev/null -w '%{http_code}' "$sink/?bytes=100000")
	[ "$code" = 200 ] && { ok=1; echo "probe ok after ${i}s"; break; }
	i=$((i + 1)); sleep 1
done
[ $ok -eq 1 ] || { echo "ABORT: no 200 through $socks in 30s (see $clog)"; exit 1; }

# ------------------------------------------------------------------- the set --
echo "# exit=$exit_pk local=$local_commit exit_commit=$exit_commit session=$set_name route=none transport=dmsg dmsg_srv=$srv socks_srv_pk=$srv_pk socks_cli_pk=$cli_pk dport=$dport sink=$sink" > "$f"
for n in $sizes; do
	for dir in down up; do
		t=1
		while [ $t -le "$trials" ]; do
			"$here/bench.sh" "$socks" "$sink" "$n" "$dir" "$set_name-t$t" >> "$f"
			t=$((t + 1))
		done
	done
done
echo "$set_name: $(grep -vc '^#' "$f") rows, hash_ok=$(grep -v '^#' "$f" | awk -F'\t' '$8==1' | wc -l)"

# ------------------------------------------------------------------- .meta --
# Which server each side ACTUALLY sat on. dmsg logs it at Info on every session:
# "<mux> stream session initial for <server pk>" (client_sessions.go:622-728).
# With -S pinned both lines must name the same server as $srv; if they differ,
# the row rode a two-server path (peer forwarding, server_session.go:368-376)
# and is not the single-hop reference it claims to be.
cli_srvs=$(grep -o 'stream session initial for [0-9a-f]\{66\}' "$clog" |
	awk '{print $NF}' | sort -u | tr '\n' ',')
srv_srvs=$(x "journalctl -u $unit --no-pager -o cat 2>/dev/null |
grep -o 'stream session initial for [0-9a-f]\{66\}' | awk '{print \$NF}' | sort -u | tr '\n' ','")
rss_after=$(exit_rss | tr -s ' ')
{
	echo "set	$set_name"
	echo "pinned_dmsg_server	$srv"
	echo "client_side_servers	$cli_srvs"
	echo "server_side_servers	$srv_srvs"
	echo "socks_server_pk	$srv_pk"
	echo "socks_client_pk	$cli_pk"
	echo "dmsg_port	$dport"
	echo "local_socks	$socks"
	echo "exit_unit	$unit"
	echo "exit_rss_before	$rss_before"
	echo "exit_rss_after	$rss_after"
	echo "e2e_encrypted	yes — per-stream client<->client Noise (stream.go:27-36, 323, 382-406); the dmsg server relays ciphertext (server_session.go:388-451 bridgeStream, an io.Copy between two mux streams)"
} > "$meta"
cat "$meta"

# ---------------------------------------------------------------- SIZING ----
# The exit is a 2-core / 4 GB Linode whose 2026-09-16 OOM kills came from the
# visor's 3 GB cxds compaction, not from small extra processes. This one adds:
# the Go runtime of the skywire binary in a leaf subcommand, ONE pinned dmsg
# session, and per-stream buffers — yamux MaxStreamWindowSize 256 KiB and smux
# MaxReceiveBuffer 1 MiB (pkg/dmsg/dmsg/types.go:77,101) plus go-socks5's 32 KiB
# copy buffers, with one stream per in-flight SOCKS conn (bench.sh runs one at a
# time). Expect tens of MB anon RSS, well under the 384M MemoryMax the unit is
# started with, so the cap — not the exit — is what fails first if it grows.
# exit_rss_before/after in the .meta are the measurement; if RssAnon approaches
# the cap, that is a leak to report, not a limit to raise.
