#!/bin/sh
# run-ceiling.sh — the ENDPOINT CEILING: what this host, this exit and this sink
# can move at all, measured the same day as the campaign that is scored by it.
#
#   bench/run-ceiling.sh <exit pk> <out dir> [trials] [sink]
#
# WHY IT EXISTS. Two of the v4 criteria (2026-09-18) stopped being a comparison
# between two skywire numbers and became a comparison against the endpoint:
#
#   criterion 2  two concurrent uploads must reach the smaller of (the sum of the
#                two best references) and 0.95 x the measured uplink ceiling;
#   criterion 4  a composition must beat the better of its two components unless
#                that better one already saturates the endpoint (its rate is
#                >= 0.9 x the ceiling), in which case 0.95 x the ceiling is the
#                bar.
#
# Both say "the ceiling measured in the same campaign", because the link moves:
# the 50 MB download bar was 8.84, 6.63 and 5.43 in three consecutive windows of
# 2026-09-16/17. A ceiling quoted from a README is not a bar, it is a memory.
#
# HOW IT IS MEASURED. N (CEIL_N, default 3) DIRECT proxy clients — `proxy start
# --direct` with stcpr preferred, the AppDirect shortcut, one hop to the exit and
# no route group at all, which is the fastest shape skywire has — each on its own
# SOCKS port, all started and warmed before anything is timed. Then, per trial:
#
#   1. ONE client transfers alone            -> a `clients=1` row
#   2. all N transfer AT THE SAME INSTANT    -> a `clients=N` row whose
#                                               sum_MBps is the ceiling reading
#
# uploads first (kind=uplink), then downloads (kind=downlink). Every transfer is
# hash-verified against the sink exactly as bench/bench.sh verifies one: the
# sink's X-Sha256 on a download, the sink's sha256 of what it received on an
# upload. A row that did not verify is still written, with its hashes column
# saying so, and the ceiling is the MEDIAN of the concurrent sums.
#
# The single row is in the file so the growth is readable rather than asserted:
# the `# ceiling` summary line per kind says whether the sum GREW from one client
# to N and by how much. A sum that did not grow means one client already had the
# endpoint, and the concurrent number is the ceiling all the same.
#
# Artefacts:
#   <out>/ceiling.tsv   kind, clients, trial, bytes, sum_MBps, per-client rates,
#                       hashes ok/n — plus `# ceiling <kind>: ...` summary lines.
#                       bench/verdict.sh reads it when it is there and keeps its
#                       old rule, saying "no ceiling row", when it is not.
#
# Knobs: CEIL_N (3), CEIL_BYTES (50 MB, megabytes or bytes), CEIL_PORT0 (1161),
# CEIL_NAME_PREFIX (ceil). APP/APP_PORT are deliberately NOT honoured — a
# ceiling needs N distinct instances, so it cannot run through one shared
# default instance the way a single-session set can.
set -u
CLI=${CLI:-/home/d0mo/go/bin/skywire}
exit_pk=$1; out=$2; trials=${3:-2}; sink=${4:-http://127.0.0.1:18080}
here=$(dirname "$0")
mkdir -p "$out"
local_commit=$(git -C "$here/.." rev-parse --short=9 HEAD)
N=${CEIL_N:-3}
port0=${CEIL_PORT0:-1161}
prefix=${CEIL_NAME_PREFIX:-ceil}
# CEIL_BYTES takes megabytes ("50") or bytes ("50000000"), as SIZES does elsewhere
bytes=${CEIL_BYTES:-50000000}
case $bytes in *[!0-9]* | "") echo "run-ceiling: CEIL_BYTES must be a number" >&2; exit 2 ;; esac
[ "$bytes" -lt 1000 ] && bytes=$((bytes * 1000000))
tmp=$(mktemp -d); trap 'rm -rf "$tmp"' EXIT INT TERM
ceil="$out/ceiling.tsv"

name_of() { echo "$prefix$1"; }
addr_of() { echo "127.0.0.1:$((port0 + $1 - 1))"; }
stop_app() { $CLI cli proxy stop -n "$1" >/dev/null 2>&1; }
tp_of() {
	$CLI cli visor state --select transports --json 2>/dev/null |
		jq -r --arg pk "$1" --arg t "$2" '.transports[] | select(.remote_pk==$pk and .type==$t) | .id' | head -1
}
exit_commit() {
	timeout 60 $CLI cli visor state --via "dmsg://$exit_pk" --select summary --json 2>/dev/null |
		jq -r '.summary.overview.build_info.commit[0:9] // "unknown"'
}
# warm <socks> <name>: four 100 KB probes, the same gate run-refs.sh puts in
# front of a reference set. A client that cannot pass it is not measured.
warm() {
	_wbad=0
	for _ in 1 2 3 4; do
		_wc=$(curl -s -m 30 --socks5-hostname "$1" -o /dev/null -w '%{http_code}' "$sink/?bytes=100000")
		[ "$_wc" = 200 ] || _wbad=$((_wbad + 1))
	done
	echo "$2 warm: $((4 - _wbad))/4 probes ok"
	[ "$_wbad" -eq 0 ]
}
# xfer_start <idx> <down|up>: ONE curl in the background. $! is the caller's to
# collect — nothing here is a subshell, so the pid is visible to `wait`.
xfer_start() {
	_xa=$(addr_of "$1")
	case $2 in
	down)
		curl -s --socks5-hostname "$_xa" -m 900 -D "$tmp/x$1.h" -o "$tmp/x$1.b" \
			-w '%{http_code} %{size_download} %{time_total} %{speed_download}' \
			"$sink/?bytes=$bytes" > "$tmp/x$1.w" 2>/dev/null &
		;;
	up)
		curl -s --socks5-hostname "$_xa" -m 900 -o "$tmp/x$1.r" \
			-w '%{http_code} %{size_upload} %{time_total} %{speed_upload}' \
			-X POST --data-binary "@$tmp/payload" "$sink/upload" > "$tmp/x$1.w" 2>/dev/null &
		;;
	esac
}
# xfer_collect <idx> <down|up> -> "<MB/s> <hash_ok>"
xfer_collect() {
	case $2 in
	down)
		_cwant=$(tr -d '\r' < "$tmp/x$1.h" 2>/dev/null | awk 'tolower($1)=="x-sha256:"{print $2}')
		_chave=$(sha256sum "$tmp/x$1.b" 2>/dev/null | cut -d' ' -f1)
		;;
	up)
		_cwant=$(jq -r .sha256 "$tmp/x$1.r" 2>/dev/null)
		_chave=$payload_sha
		;;
	esac
	_cok=0; [ -n "$_cwant" ] && [ "$_cwant" = "$_chave" ] && _cok=1
	# shellcheck disable=SC2046 # the four -w fields are split on purpose
	set -- $(cat "$tmp/x$1.w" 2>/dev/null)
	printf '%s %s\n' "$(awk -v s="${4:-0}" 'BEGIN{printf "%.2f", s/1e6}')" "$_cok"
}
# ceil_row <kind> <down|up> <clients> <trial>: one line of ceiling.tsv. The
# clients run AT THE SAME INSTANT — every curl is launched before any is waited
# on — and the payload of an upload is generated once, outside every timed
# section, exactly as the two-upload cell of run-mux.sh does it.
ceil_row() {
	_rk=$1; _rd=$2; _rn=$3; _rt=$4
	_rpids=""; _ri=1
	while [ "$_ri" -le "$_rn" ]; do
		xfer_start "$_ri" "$_rd"; _rpids="$_rpids $!"
		_ri=$((_ri + 1))
	done
	for _rp in $_rpids; do wait "$_rp"; done
	_rsum=0; _rrates=""; _rok=0; _ri=1
	while [ "$_ri" -le "$_rn" ]; do
		_rres=$(xfer_collect "$_ri" "$_rd")
		_rr=${_rres%% *}; _ro=${_rres##* }
		_rsum=$(awk -v a="$_rsum" -v b="$_rr" 'BEGIN{printf "%.2f", a + b}')
		_rrates="${_rrates:+$_rrates,}$_rr"
		[ "$_ro" = 1 ] && _rok=$((_rok + 1))
		_ri=$((_ri + 1))
	done
	printf '%s\t%s\t%s\t%s\t%s\t%s\t%s\n' "$_rk" "$_rn" "$_rt" "$bytes" "$_rsum" "$_rrates" "$_rok/$_rn" >> "$ceil"
	echo "$_rk trial $_rt: $_rn client(s) summed $_rsum MB/s ($_rrates), $_rok/$_rn hash-verified"
}
# ceil_median <kind> <clients> -> the median sum over that kind's rows, or "-"
ceil_median() {
	awk -F'\t' -v k="$1" -v c="$2" '!/^#/ && $1==k && $2+0==c+0 {v[++n]=$5+0}
		END{
			if (!n) {print "-"; exit}
			for (i = 2; i <= n; i++) {x=v[i]; j=i-1; while (j>0 && v[j]>x) {v[j+1]=v[j]; j--} v[j+1]=x}
			printf "%.2f", (n%2)?v[(n+1)/2]:(v[n/2]+v[n/2+1])/2
		}' "$ceil"
}

ec=$(exit_commit)
echo "local=$local_commit exit=$ec clients=$N size=$bytes trials=$trials sink=$sink"

# --- N direct clients, all up and warm before anything is timed ----------------
$CLI cli route settings --prefer default >/dev/null 2>&1
up_clients=0; tps=""
i=1
while [ "$i" -le "$N" ]; do
	name=$(name_of "$i"); socks=$(addr_of "$i")
	stop_app "$name"
	timeout 180 $CLI cli proxy start -k "$exit_pk" -n "$name" -a "$socks" --direct >/dev/null 2>&1
	if warm "$socks" "$name"; then
		up_clients=$((up_clients + 1))
	else
		echo "$name: probes failing — the ceiling is measured with it anyway, its rate will show it"
	fi
	tps="$tps $name=$(tp_of "$exit_pk" stcpr)"
	i=$((i + 1))
done
[ "$up_clients" -ge 1 ] || { echo "run-ceiling: no direct client came up — nothing to measure"; i=1; while [ "$i" -le "$N" ]; do stop_app "$(name_of "$i")"; i=$((i + 1)); done; exit 1; }

head -c "$bytes" /dev/urandom > "$tmp/payload"
payload_sha=$(sha256sum "$tmp/payload" | cut -d' ' -f1)

{
	printf '# ceiling.tsv — the endpoint ceiling: %s concurrent DIRECT clients, %s bytes each, %s trial(s)\n' "$N" "$bytes" "$trials"
	printf '# exit=%s local=%s exit_commit=%s sink=%s clients_warm=%s/%s stcpr tps:%s\n' \
		"$exit_pk" "$local_commit" "$ec" "$sink" "$up_clients" "$N" "$tps"
	printf '# a clients=1 row is the same transfer alone, immediately before the concurrent one of the same trial\n'
	printf '# kind\tclients\ttrial\tbytes\tsum_MBps\trates_MBps\thashes_ok/n\n'
} > "$ceil"

t=1
while [ "$t" -le "$trials" ]; do
	ceil_row uplink up 1 "$t"
	ceil_row uplink up "$N" "$t"
	ceil_row downlink down 1 "$t"
	ceil_row downlink down "$N" "$t"
	t=$((t + 1))
done

i=1
while [ "$i" -le "$N" ]; do stop_app "$(name_of "$i")"; i=$((i + 1)); done

for k in uplink downlink; do
	one=$(ceil_median "$k" 1); many=$(ceil_median "$k" "$N")
	grew=$(awk -v a="$one" -v b="$many" 'BEGIN{if (a=="-"||b=="-") {print "unknown"; exit} printf "%s (x%.2f)", (b+0 > a+0 ? "yes" : "no"), (a+0>0 ? b/a : 0)}')
	printf '# ceiling %s: single %s MB/s, %s concurrent %s MB/s, grew=%s — the ceiling is the concurrent median\n' \
		"$k" "$one" "$N" "$many" "$grew" >> "$ceil"
	echo "ceiling $k: single $one, $N concurrent $many MB/s, grew=$grew"
done
echo "--- $ceil"
cat "$ceil"
