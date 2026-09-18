#!/bin/sh
# run-ceiling.sh — the ENDPOINT CEILING: what this host, this exit and this sink
# can move at all, measured the same day as the campaign that is scored by it.
#
#   bench/run-ceiling.sh <exit pk> <out dir> [trials] [sink] [pins dir]
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
# WHICH ROUTES IT RIDES (2026-09-17). The first live run measured everything on
# DIRECT clients and the downlink came out at 4.72 single / 5.04 concurrent MB/s
# while a single download through the best intermediate (via-0371ab4b) was doing
# 8-9.5 MB/s in the same hour: the direct stcpr path is ITSELF the download
# bottleneck, so a downlink ceiling taken through it measures that path, not the
# endpoint, and would let the composition criterion pass trivially. So:
#
#   downlink   the N clients are pinned to the N best DISTINCT routes of the
#              paired ranking — slot 1 the best, slot 2 the second best, and so
#              on, `direct` appended last when the ranking holds fewer than N.
#              The concurrent sum is then what the endpoint can push to this
#              host over the best paths it has.
#   uplink     DIRECT clients, unchanged: the uplink is the local card and the
#              direct path has never been the thing limiting it (9.04 single /
#              10.70 concurrent in the same run). One extra `uplink-via` row per
#              trial sends over the best via route ALONE, so the file itself
#              says whether the direct uplink is a bottleneck too.
#
# The ranking is read exactly as lib-paired.sh resolves a reference: the out dir
# then its parent, `paired-ref.tsv` (bench/pick-ref.sh's contemporaneous probe)
# first and `paired_rank` (the ref-*.tsv / drift.tsv medians) second — a ceiling
# usually runs into a directory of its own beside the campaign that ranked the
# routes. CEIL_ROUTES overrides the list outright.
#
# HOW IT IS MEASURED. N (CEIL_N, default 3) proxy clients, each on its own SOCKS
# port, all started and warmed before anything is timed. A direct client is
# `proxy start --direct` with stcpr preferred, the AppDirect shortcut, one hop to
# the exit and no route group at all; a via client is `--route <pins>/via-<short>
# .json` with the pinned leg verified the way lib-paired.sh verifies it. Then,
# per trial and per kind:
#
#   1. ONE client transfers alone            -> a `clients=1` row
#   2. all N transfer AT THE SAME INSTANT    -> a `clients=N` row whose
#                                               sum_MBps is the ceiling reading
#
# uploads first (kind=uplink, direct clients), then downloads (kind=downlink,
# best-route clients) — each kind takes its clients up, runs all its trials and
# puts them down, because the two kinds no longer ride the same instances. Every
# transfer is hash-verified against the sink exactly as bench/bench.sh verifies
# one: the sink's X-Sha256 on a download, the sink's sha256 of what it received
# on an upload. A row that did not verify is still written, with its hashes
# column saying so, and the ceiling is the MEDIAN of the concurrent sums.
#
# The single row is in the file so the growth is readable rather than asserted:
# the `# ceiling` summary line per kind says whether the sum GREW from one client
# to N and by how much. A sum that did not grow means one client already had the
# endpoint, and the concurrent number is the ceiling all the same.
#
# Artefacts:
#   <out>/ceiling.tsv   kind, clients, trial, bytes, sum_MBps, per-client
#                       `<route>:<rate>` pairs, hashes ok/n — plus `# ceiling
#                       <kind>: ...` summary lines. bench/verdict.sh reads it
#                       when it is there and keeps its old rule, saying "no
#                       ceiling row", when it is not.
#
# Knobs: CEIL_N (3), CEIL_BYTES (50 MB, megabytes or bytes), CEIL_PORT0 (1161),
# CEIL_NAME_PREFIX (ceil), CEIL_ROUTES (the downlink route list, space- or
# comma-separated, `direct` or a pin short), CEIL_PINS (the pin directory, also
# the 5th argument). APP/APP_PORT are deliberately NOT honoured — a ceiling needs
# N distinct instances, so it cannot run through one shared default instance the
# way a single-session set can.
set -u
CLI=${CLI:-/home/d0mo/go/bin/skywire}
exit_pk=$1; out=$2; trials=${3:-2}; sink=${4:-http://127.0.0.1:18080}
here=$(dirname "$0")
pins=${5:-${CEIL_PINS:-${PINS:-$here/pins}}}
PAIRED_HERE=$here
export PAIRED_HERE
# shellcheck source=bench/lib-paired.sh
. "$here/lib-paired.sh"
# shellcheck source=bench/lib-blackout.sh
. "$here/lib-blackout.sh"
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
# route_label <token>: how a route is named in the rates column and the header.
route_label() {
	case $1 in
	direct) echo direct ;;
	*/*) echo "via-$(basename "$1" .json | sed 's/^via-//')" ;;
	*) echo "via-$1" ;;
	esac
}
label_of() { route_label "$(cat "$tmp/route.$1" 2>/dev/null || echo direct)"; }
tp_of() {
	$CLI cli visor state --select transports --json 2>/dev/null |
		jq -r --arg pk "$1" --arg t "$2" '.transports[] | select(.remote_pk==$pk and .type==$t) | .id' | head -1
}
exit_commit() {
	timeout 60 $CLI cli visor state --via "dmsg://$exit_pk" --select summary --json 2>/dev/null |
		jq -r '.summary.overview.build_info.commit[0:9] // "unknown"'
}
# ceil_rank: the paired ranking, best route first, one token per line. The same
# sources and the same order lib-paired.sh's paired_resolve uses, tried in the
# out dir and then its parent: <dir>/paired-ref.tsv (bench/pick-ref.sh's live
# probe — its rows are in probe order, so they are re-sorted by the probed
# median and the candidates that never verified are dropped), then paired_rank
# over the same dir (the ref-*.tsv and drift.tsv medians).
ceil_rank() {
	for _cd in "$out" "$(dirname "$out")"; do
		if [ -s "$_cd/paired-ref.tsv" ]; then
			_cr=$(grep -v '^#' "$_cd/paired-ref.tsv" |
				awk -F'\t' '$2 != "-" && $2 + 0 > 0 && $3 !~ /^0\// {print $2 "\t" $1}' |
				sort -k1,1 -rn | awk '!seen[$2]++ {print $2}')
			[ -n "$_cr" ] && { echo "$_cr"; return 0; }
		fi
		_cr=$(paired_rank "$_cd" | awk '{print $2}')
		[ -n "$_cr" ] && { echo "$_cr"; return 0; }
	done
	return 0
}
# ceil_routes <n>: the n routes the downlink clients ride, one per line, best
# first. `direct` is appended when the ranking holds fewer than n distinct
# routes, and repeated to fill the rest — with no ranking at all this is exactly
# the pre-2026-09-17 behaviour, n direct clients.
ceil_routes() {
	_cn=$1
	if [ -n "${CEIL_ROUTES:-}" ]; then
		_cl=$(echo "$CEIL_ROUTES" | tr ' ,' '\n\n' | grep -v '^$' | awk '!seen[$0]++' | head -n "$_cn")
	else
		_cl=$( { ceil_rank; echo direct; } | awk '!seen[$0]++' | head -n "$_cn")
	fi
	_ci=$(echo "$_cl" | grep -c .)
	while [ "$_ci" -lt "$_cn" ]; do
		_cl="$_cl
direct"
		_ci=$((_ci + 1))
	done
	echo "$_cl"
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
# start_client <idx> <route token>: one proxy instance on that one route, warmed.
# `direct` is the AppDirect shortcut; anything else is a `--route` pin, whose leg
# is checked against the pin file the way lib-paired.sh's paired_start checks it.
# A route with no pin file falls back to direct rather than skipping the slot.
start_client() {
	_si=$1; _st=$2
	_sn=$(name_of "$_si"); _sa=$(addr_of "$_si")
	stop_app "$_sn"
	case $_st in
	direct)
		$CLI cli route settings --prefer default >/dev/null 2>&1
		timeout 240 $CLI cli proxy start -k "$exit_pk" -n "$_sn" -a "$_sa" --direct >/dev/null 2>&1
		;;
	*)
		_sp=$_st
		[ -f "$_sp" ] || _sp="$pins/via-$_st.json"
		if [ ! -f "$_sp" ]; then
			echo "run-ceiling: no pin file for route '$_st' (pins=$pins) — client $_si falls back to direct"
			start_client "$_si" direct
			return $?
		fi
		timeout 240 $CLI cli proxy start -k "$exit_pk" -n "$_sn" -a "$_sa" --route "$_sp" >/dev/null 2>&1
		_swant=$(jq -r '.[0].forward[0].TpID' "$_sp" 2>/dev/null)
		_shave=$($CLI cli proxy mux info -n "$_sn" --json 2>/dev/null | jq -r '.[0].legs[]?.transport_id')
		echo "$_shave" | grep -q "^$_swant" ||
			echo "run-ceiling: $_sn pinned leg $_swant NOT present (legs: $(echo "$_shave" | tr '\n' ' ')) — measured anyway, its rate will show it"
		;;
	esac
	echo "$_st" > "$tmp/route.$_si"
	warm "$_sa" "$_sn on $(route_label "$_st")"
}
# start_clients <route list>: bring up one client per route, in order, and echo
# how many passed their probes.
start_clients() {
	_cu=0; _cix=1
	for _ct in $1; do
		if start_client "$_cix" "$_ct"; then
			_cu=$((_cu + 1))
		else
			echo "$(name_of "$_cix"): probes failing — the ceiling is measured with it anyway, its rate will show it"
		fi
		_cix=$((_cix + 1))
	done
	echo "$_cu" > "$tmp/warmcount"
}
stop_clients() { _sx=1; while [ "$_sx" -le "$N" ]; do stop_app "$(name_of "$_sx")"; _sx=$((_sx + 1)); done; }
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
# section, exactly as the two-upload cell of run-mux.sh does it. Each rate in
# the rates column is prefixed with the route that client rides.
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
		_rrates="${_rrates:+$_rrates,}$(label_of "$_ri"):$_rr"
		[ "$_ro" = 1 ] && _rok=$((_rok + 1))
		_ri=$((_ri + 1))
	done
	printf '%s\t%s\t%s\t%s\t%s\t%s\t%s\n' "$_rk" "$_rn" "$_rt" "$bytes" "$_rsum" "$_rrates" "$_rok/$_rn" >> "$ceil"
	echo "$_rk trial $_rt: $_rn client(s) summed $_rsum MB/s ($_rrates), $_rok/$_rn hash-verified"
	# A client that did not verify is this run's failed row: take the local
	# receive-loop proof before it clears (bench/lib-blackout.sh, into
	# ceiling.recovery.tsv and ceiling.row<kind>-c<n>-t<trial>.* beside it).
	[ "$_rok" -lt "$_rn" ] && blackout_capture "$out" ceiling "$_rk-c$_rn-t$_rt"
	return 0
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
down_routes=$(ceil_routes "$N")
down_labels=$(for r in $down_routes; do route_label "$r"; done | tr '\n' ' ' | sed 's/ *$//')
best_route=$(echo "$down_routes" | head -1)
echo "local=$local_commit exit=$ec clients=$N size=$bytes trials=$trials sink=$sink"
echo "downlink routes (best first): $down_labels"

head -c "$bytes" /dev/urandom > "$tmp/payload"
payload_sha=$(sha256sum "$tmp/payload" | cut -d' ' -f1)

# --- uplink: N DIRECT clients. The uplink is the local card ------------------
up_list=""; i=1
while [ "$i" -le "$N" ]; do up_list="$up_list direct"; i=$((i + 1)); done
start_clients "$up_list"
warm_up=$(cat "$tmp/warmcount")
tps=""; i=1
while [ "$i" -le "$N" ]; do tps="$tps $(name_of "$i")=$(tp_of "$exit_pk" stcpr)"; i=$((i + 1)); done

{
	printf '# ceiling.tsv — the endpoint ceiling: %s concurrent clients, %s bytes each, %s trial(s)\n' "$N" "$bytes" "$trials"
	printf '# exit=%s local=%s exit_commit=%s sink=%s\n' "$exit_pk" "$local_commit" "$ec" "$sink"
	printf '# uplink rows: %s DIRECT clients (the uplink is the local card), warm=%s/%s, stcpr tps:%s\n' \
		"$N" "$warm_up" "$N" "$tps"
	printf '# downlink rows: the %s best DISTINCT routes of the paired ranking, slot 1 first — %s\n' "$N" "$down_labels"
	printf '# uplink-via rows: one upload over %s alone, so the direct uplink can be seen for what it is\n' "$(route_label "$best_route")"
	printf '# a clients=1 row is the same transfer alone, immediately before the concurrent one of the same trial\n'
	printf '# kind\tclients\ttrial\tbytes\tsum_MBps\troute:rate_MBps\thashes_ok/n\n'
} > "$ceil"

if [ "$warm_up" -ge 1 ]; then
	t=1
	while [ "$t" -le "$trials" ]; do
		ceil_row uplink up 1 "$t"
		ceil_row uplink up "$N" "$t"
		t=$((t + 1))
	done
else
	echo "run-ceiling: no direct client came up — no uplink rows"
	printf '# no uplink rows: no direct client passed its probes\n' >> "$ceil"
fi
stop_clients

# --- downlink: the N best distinct routes ------------------------------------
start_clients "$down_routes"
warm_down=$(cat "$tmp/warmcount")
printf '# downlink clients warm=%s/%s on %s\n' "$warm_down" "$N" "$down_labels" >> "$ceil"
if [ "$warm_down" -ge 1 ]; then
	t=1
	while [ "$t" -le "$trials" ]; do
		ceil_row downlink down 1 "$t"
		ceil_row downlink down "$N" "$t"
		# the same best route, uploading alone: is the direct uplink the bottleneck?
		[ "$best_route" = direct ] || ceil_row uplink-via up 1 "$t"
		t=$((t + 1))
	done
else
	echo "run-ceiling: no downlink client came up — no downlink rows"
	printf '# no downlink rows: no client passed its probes on %s\n' "$down_labels" >> "$ceil"
fi
stop_clients

[ "$warm_up" -ge 1 ] || [ "$warm_down" -ge 1 ] || { echo "run-ceiling: nothing came up — nothing to measure"; exit 1; }

for k in uplink downlink; do
	one=$(ceil_median "$k" 1); many=$(ceil_median "$k" "$N")
	[ "$one" = - ] && [ "$many" = - ] && continue
	grew=$(awk -v a="$one" -v b="$many" 'BEGIN{if (a=="-"||b=="-") {print "unknown"; exit} printf "%s (x%.2f)", (b+0 > a+0 ? "yes" : "no"), (a+0>0 ? b/a : 0)}')
	case $k in
	uplink) via="over $N direct clients" ;;
	*) via="over $down_labels" ;;
	esac
	printf '# ceiling %s: single %s MB/s, %s concurrent %s MB/s %s, grew=%s — the ceiling is the concurrent median\n' \
		"$k" "$one" "$N" "$many" "$via" "$grew" >> "$ceil"
	echo "ceiling $k: single $one, $N concurrent $many MB/s $via, grew=$grew"
done
uv=$(ceil_median uplink-via 1)
if [ "$uv" != - ]; then
	ud=$(ceil_median uplink 1)
	printf '# uplink over %s alone: %s MB/s vs %s MB/s direct — %s\n' \
		"$(route_label "$best_route")" "$uv" "$ud" \
		"$(awk -v a="$uv" -v b="$ud" 'BEGIN{if (b=="-") {print "no direct row to compare"; exit} printf "%s", (a+0 > b+0 ? "the direct uplink IS a bottleneck" : "the direct uplink is not the bottleneck")}')" >> "$ceil"
	echo "uplink via $(route_label "$best_route") alone: $uv MB/s (direct single $ud)"
fi
echo "--- $ceil"
cat "$ceil"
