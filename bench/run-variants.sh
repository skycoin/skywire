#!/bin/sh
# run-variants.sh — ONE deploy, ONE session, many knob variants, A interleaved
# with every B.
#
#   bench/run-variants.sh <exit pk> <out dir> <pins dir> [trials] [sink] [pin order] [variants file]
#
# e.g.
#   bench/run-variants.sh 0227…80b1 bench/2026-09-18/<commit>-sweep $S/pins \
#       3 http://127.0.0.1:18080 "" bench/screen/factors-mux.variants
#
# WHY. A knob decision used to cost a build, a deploy and a campaign — about
# fifty minutes for ONE value — even though 115 router knobs and 71 proxy knobs
# are LIVE: `route settings k=v` and `proxy settings k=v` move them on a running
# visor and a running app. This runner spends the deploy once. The session is
# dialed once, and between rows — never during one — it writes the next
# variant's knobs, reads them back, and measures one row. Nothing is rebuilt,
# nothing is restarted, and the app under test never re-dials, so a row costs
# its transfer and about a second of settling.
#
# THE SHAPE OF A RUN. The variants file is one variant per line:
#
#   baseline
#   wide      ecf.max_window_bytes=16MiB
#   wide+conc ecf.max_window_bytes=16MiB upload.concurrency=8
#
# Line 1 — or whichever line is named `baseline` — is variant A, the control.
# Every other line is a variant B. For each cell (SWEEP_SIZES x SWEEP_DIRS) and
# each trial, the runner walks the B list and, for each B, measures
#
#   apply A's knobs -> one row -> apply B's knobs -> one row
#
# so every B row has an A row measured SECONDS BEFORE IT on the same session.
# That is the whole point: the bar on this link swings 2x inside an hour, so an
# A row from ten minutes ago is not a control. No reference route is needed and
# no paired reference instance is started — A IS the reference, and it is the
# same session, the same tunnels and the same minute as B.
#
# A's knobs are re-applied on every A row rather than assumed: a variant's apply
# writes the FULL assignment — every key any variant mentions, at that variant's
# value or, where the variant does not name it, at the value the knob held when
# the run started — so B's knobs can never leak into an A row.
#
# BOTH ENDS. A knob whose name is in the router catalog is written to the local
# visor AND, over `--via dmsg://<exit>`, to the exit's: a send window or a RACK
# term that moves on one end only measures half a path. A knob in the proxy
# catalog goes to `proxy settings --app <app>` on this end, where the app is.
# Membership is decided ONCE at the start of the run from `route settings --json`
# and `proxy settings --json` themselves — never from the name — and a key in
# neither catalog stops the run before a single row is measured.
#
# APPLY, THEN VERIFY. After each apply the knobs are read back out of the same
# two --json views and recorded in the row (the `knobs` column), with any
# mismatch named in the `note` column. A knob whose catalog doc says it is read
# when a route group is BUILT or when a dial CHOOSES a route cannot bite a
# session that is already up; those are noted as `dialtime:<key>` in every row
# that carries them rather than silently measured as if they had.
#
# RESTORE. Every key the run touches is snapshotted before the first row and put
# back at the end — on both ends — from the EXIT and TERM/INT traps as well, so a
# killed run does not leave the visor tuned. The restore table is
# <out>/sweep/knobs-restore.tsv and it is re-read, not remembered, so a restore
# is verifiable after the fact.
#
# ARTEFACTS
#   <out>/<variant>.tsv           ordinary bench.sh rows — bench/summarize.sh
#   <out>/<variant>.carrier.tsv   and bench/verdict.sh read these unchanged
#   <out>/sweep/rows.tsv          one line per row: variant, cell, trial, pair,
#                                 MB/s, hash_ok, the knobs read back, notes
#   <out>/sweep/verdict.tsv       per variant and cell: median(B)/median(A), the
#                                 per-pair ratios, hashes, wire/goodput, PASS/FAIL
#   <out>/sweep/knobs-restore.tsv what every touched knob held before the run
#   <out>/exit-resources.tsv      the exit RssAnon/CPU gate (EXIT_RES=0 turns off)
#
# KNOBS OF THE RUNNER ITSELF
#   SWEEP_SIZES   cells, megabytes or bytes (default "50")
#   SWEEP_DIRS    "down up"
#   SWEEP_TRIALS  A/B pairs per variant per cell (default 3, or the 4th argument)
#   SWEEP_BAR     the ratio a variant must reach to PASS (default 1.0)
#   SWEEP_APP     the app instance to measure through (default `sweep`, on
#                 SWEEP_PORT 1171) — APP/APP_PORT are honoured the way the other
#                 runners honour them, to drive the operator's own :1080 session
#   SWEEP_TUNNELS tunnels the session is dialed with (default 2)
#   SWEEP_ROUTE   a pin short from <pins dir> to dial the session on instead of
#                 the default pool (read-only: nothing here writes a pin)
#   SWEEP_DRY=1   run against bench/dry-stub-cli.sh and bench/dry-stub-bench.sh
#                 instead of the rig — the interleaving, the restore and the
#                 verdict arithmetic, with no visor and no network. The
#                 self-test is bench/selftest-variants.sh.
#
# This is the A/B half of knob work. The one-knob-many-values form, which spends
# a COMPLETE runner invocation per value, is still bench/run-sweep.sh; the
# screening design that says which knobs deserve either is bench/screen.
set -u
exit_pk=${1:?usage: run-variants.sh <exit pk> <out dir> <pins dir> [trials] [sink] [pin order] [variants]}
out=${2:?no out dir}
here=$(dirname "$0")
pins=${3:-$here/pins}
trials=${SWEEP_TRIALS:-${4:-3}}
sink=${5:-http://127.0.0.1:18080}
# shellcheck disable=SC2034 # accepted for runner-signature parity; this runner pins nothing
_order=${6:-}
variants=${7:-${SWEEP_VARIANTS:-$out/variants.txt}}
SWEEP_DRY=${SWEEP_DRY:-0}
SWEEP_DRY_STATE=${SWEEP_DRY_STATE:-$out/dry}
if [ "$SWEEP_DRY" = 1 ]; then
	export SWEEP_DRY_STATE
	mkdir -p "$SWEEP_DRY_STATE"
	CLI=${CLI:-$here/dry-stub-cli.sh}
	BENCH=${BENCH:-$here/dry-stub-bench.sh}
	EXIT_RES=${EXIT_RES:-0}
	SWEEP_WARM=${SWEEP_WARM:-0}
	SWEEP_KNOB_WAIT=${SWEEP_KNOB_WAIT:-0}
	SWEEP_SETTLE=${SWEEP_SETTLE:-0}
fi
CLI=${CLI:-/home/d0mo/go/bin/skywire}
BENCH=${BENCH:-$here/bench.sh}
EXIT_RES=${EXIT_RES:-1}
SWEEP_WARM=${SWEEP_WARM:-1}
sizes=${SWEEP_SIZES:-50}
dirs=${SWEEP_DIRS:-down up}
bar=${SWEEP_BAR:-1.0}
app=${APP:-${SWEEP_APP:-sweep}}
port=${APP_PORT:-${SWEEP_PORT:-1171}}
socks=127.0.0.1:$port
tunnels=${SWEEP_TUNNELS:-2}
sw="$out/sweep"
mkdir -p "$sw"
tmp=$(mktemp -d)
exit_ok=0
exit_res_fail=0

# --- knob state ----------------------------------------------------------------
# scope is `router` (both visors) or `proxy` (this end's app). The restore table
# is written before the first row and read back by restore_knobs, which is also
# the EXIT/TERM/INT trap, so an interrupted run leaves the visors as it found
# them.
restore_file="$sw/knobs-restore.tsv"
restored=0
route_json() { # [via]
	if [ -n "${1:-}" ]; then timeout 120 "$CLI" cli --via "dmsg://$1" route settings --json 2>/dev/null
	else "$CLI" cli route settings --json 2>/dev/null; fi
}
proxy_json() { "$CLI" cli proxy settings --app "$app" --json 2>/dev/null; }
route_get() { jq -r --arg k "$2" '.knobs[$k] // empty' "$1" 2>/dev/null; }
proxy_get() { jq -r --arg k "$2" '.knobs[]? | select(.name == $k) | .value' "$1" 2>/dev/null; }
# knob_doc <key>: the catalog doc, whichever catalog holds it.
knob_doc() {
	_d=$(jq -r --arg k "$1" '.knob_detail[$k].doc // empty' "$sw/route.json" 2>/dev/null)
	[ -n "$_d" ] || _d=$(jq -r --arg k "$1" '.knobs[]? | select(.name == $k) | .doc' "$sw/proxy.json" 2>/dev/null)
	echo "$_d"
}
# dial_time <key>: does the catalog say this knob is read when a group is BUILT
# or when a dial CHOOSES a route? Such a knob cannot move a session that is
# already up, and the row says so instead of pretending otherwise.
dial_time() {
	knob_doc "$1" | grep -qi "NEW mux route group\|NEW route group\|a dial is CHOOSING\|at dial time\|created after it\|when a transport first forwards"
}
restore_knobs() {
	[ "$restored" = 0 ] || return 0
	restored=1
	[ -s "$restore_file" ] || return 0
	_rr=""; _rp=""
	while IFS='	' read -r _scope _key _val; do
		case $_scope in
		\#* | "") continue ;;
		router) _rr="$_rr $_key=$_val" ;;
		proxy) _rp="$_rp $_key=$_val" ;;
		esac
	done < "$restore_file"
	# shellcheck disable=SC2086 # deliberate word lists of key=value
	[ -n "$_rr" ] && {
		"$CLI" cli route settings $_rr >/dev/null 2>&1 || echo "run-variants: local router restore REFUSED:$_rr"
		[ "$exit_ok" = 1 ] && { timeout 120 "$CLI" cli --via "dmsg://$exit_pk" route settings $_rr >/dev/null 2>&1 || echo "run-variants: EXIT router restore REFUSED:$_rr"; }
	}
	# shellcheck disable=SC2086
	[ -n "$_rp" ] && { "$CLI" cli proxy settings --app "$app" $_rp >/dev/null 2>&1 || echo "run-variants: proxy restore REFUSED:$_rp"; }
	echo "run-variants: knobs restored (${_rr:-no router knob})${_rp:+, proxy:$_rp}"
	return 0
}
# shellcheck disable=SC2329 # invoked by the EXIT/INT/TERM trap below
cleanup() { restore_knobs; [ "${SWEEP_KEEP_APP:-0}" = 1 ] || "$CLI" cli proxy stop -n "$app" >/dev/null 2>&1; rm -rf "$tmp"; }
trap cleanup EXIT INT TERM

# --- the variants file ---------------------------------------------------------
[ -f "$variants" ] || { echo "run-variants: no variants file '$variants'" >&2; exit 2; }
grep -v '^[[:space:]]*#' "$variants" | grep -v '^[[:space:]]*$' > "$tmp/v" || true
[ -s "$tmp/v" ] || { echo "run-variants: '$variants' holds no variants" >&2; exit 2; }
# variant A: the line named `baseline`, else line 1.
a_name=$(awk '$1 == "baseline" {print $1; exit}' "$tmp/v")
[ -n "$a_name" ] || a_name=$(awk 'NR == 1 {print $1}' "$tmp/v")
: > "$tmp/names"
while read -r line; do
	# shellcheck disable=SC2086 # the variant line IS a word list: name then key=value pairs
	set -- $line
	nm=$1; shift
	case $nm in
	*[!A-Za-z0-9._+-]*) echo "run-variants: variant name '$nm' is not [A-Za-z0-9._+-]" >&2; exit 2 ;;
	esac
	grep -qx "$nm" "$tmp/names" && { echo "run-variants: duplicate variant '$nm'" >&2; exit 2; }
	echo "$nm" >> "$tmp/names"
	echo "$*" > "$tmp/knobs.$nm"
	for kv in "$@"; do
		case $kv in
		*=*) echo "${kv%%=*}" >> "$tmp/keys" ;;
		*) echo "run-variants: '$kv' in variant '$nm' is not key=value" >&2; exit 2 ;;
		esac
	done
done < "$tmp/v"
sort -u "$tmp/keys" 2>/dev/null > "$tmp/keys.u" || : > "$tmp/keys.u"
b_list=$(grep -vx "$a_name" "$tmp/names" | tr '\n' ' ')
[ -n "$b_list" ] || { echo "run-variants: '$variants' holds only the baseline" >&2; exit 2; }

# --- catalogs: which knob belongs where, decided once --------------------------
route_json "" > "$sw/route.json"
[ -s "$sw/route.json" ] || { echo "run-variants: 'route settings --json' returned nothing — is the visor up?" >&2; exit 2; }
exit_ok=0
if route_json "$exit_pk" > "$sw/route-exit.json" && [ -s "$sw/route-exit.json" ]; then
	exit_ok=1
else
	echo "run-variants: the EXIT's router catalog could not be read over dmsg://$exit_pk — router knobs move on THIS END ONLY and every row says so"
fi
"$CLI" cli proxy start -k "$exit_pk" -n "$app" -a "$socks" --tunnels "$tunnels" \
	${SWEEP_ROUTE:+--route "$pins/via-$SWEEP_ROUTE.json"} >/dev/null 2>&1
sleep "${SWEEP_SETTLE:-5}"
proxy_json > "$sw/proxy.json"
[ -s "$sw/proxy.json" ] || { echo "run-variants: 'proxy settings --app $app --json' returned nothing — did the app come up?" >&2; exit 2; }
: > "$tmp/scope"
bad=""
while read -r k; do
	[ -n "$k" ] || continue
	if [ -n "$(route_get "$sw/route.json" "$k")" ]; then echo "$k	router" >> "$tmp/scope"
	elif [ -n "$(proxy_get "$sw/proxy.json" "$k")" ]; then echo "$k	proxy" >> "$tmp/scope"
	else bad="$bad $k"; fi
done < "$tmp/keys.u"
[ -z "$bad" ] || { echo "run-variants: not in either catalog:$bad — nothing was measured" >&2; exit 2; }
scope_of() { awk -F'\t' -v k="$1" '$1 == k {print $2; exit}' "$tmp/scope"; }

# --- snapshot, for the restore and for the implicit value of an unnamed knob ---
{
	printf '# what every knob this run touches held BEFORE it; run-variants restores exactly this\n'
	printf '# scope\tkey\tvalue\n'
	while read -r k; do
		[ -n "$k" ] || continue
		s=$(scope_of "$k")
		if [ "$s" = router ]; then v=$(route_get "$sw/route.json" "$k"); else v=$(proxy_get "$sw/proxy.json" "$k"); fi
		printf '%s\t%s\t%s\n' "$s" "$k" "$v"
	done < "$tmp/keys.u"
} > "$restore_file"
base_of() { awk -F'\t' -v k="$1" '$2 == k {print $3; exit}' "$restore_file"; }
dial_keys=$(while read -r k; do [ -n "$k" ] && dial_time "$k" && printf '%s,' "$k"; done < "$tmp/keys.u")
dial_keys=${dial_keys%,}
[ -z "$dial_keys" ] || echo "run-variants: dial-time knob(s) $dial_keys cannot move a session that is already up — every row carrying them says dialtime"

# --- apply -------------------------------------------------------------------
# apply <variant>: write the FULL assignment — every key any variant names, at
# this variant's value or at the value it held before the run — then read it all
# back. Leaves $apply_read (the readback, k=v,k=v) and $apply_note.
apply() {
	_av=$1; _ar=""; _ap=""; _want=""
	while read -r _k; do
		[ -n "$_k" ] || continue
		_kval=$(awk -v k="$_k" '{for (i = 1; i <= NF; i++) {split($i, p, "="); if (p[1] == k) {sub(/^[^=]*=/, "", $i); print $i; exit}}}' "$tmp/knobs.$_av")
		[ -n "$_kval" ] || _kval=$(base_of "$_k")
		[ -n "$_kval" ] || continue
		_want="$_want $_k=$_kval"
		if [ "$(scope_of "$_k")" = router ]; then _ar="$_ar $_k=$_kval"; else _ap="$_ap $_k=$_kval"; fi
	done < "$tmp/keys.u"
	# shellcheck disable=SC2086 # deliberate word lists of key=value
	if [ -n "$_ar" ]; then
		"$CLI" cli route settings $_ar >/dev/null 2>&1 || echo "$_av: local 'route settings$_ar' REFUSED"
		[ "$exit_ok" = 1 ] && { timeout 120 "$CLI" cli --via "dmsg://$exit_pk" route settings $_ar >/dev/null 2>&1 || echo "$_av: EXIT 'route settings$_ar' REFUSED"; }
	fi
	# shellcheck disable=SC2086
	[ -n "$_ap" ] && { "$CLI" cli proxy settings --app "$app" $_ap >/dev/null 2>&1 || echo "$_av: 'proxy settings$_ap' REFUSED"; }
	sleep "${SWEEP_KNOB_WAIT:-6}"   # one app pull tick (tunnel.probe_interval, 5 s)
	route_json "" > "$tmp/route.now"
	proxy_json > "$tmp/proxy.now"
	apply_read=""; apply_note=""
	for _kv in $_want; do
		_k=${_kv%%=*}; _kval=${_kv#*=}
		if [ "$(scope_of "$_k")" = router ]; then _got=$(route_get "$tmp/route.now" "$_k"); else _got=$(proxy_get "$tmp/proxy.now" "$_k"); fi
		apply_read="${apply_read:+$apply_read,}$_k=$_got"
		_same=$(awk -v a="$_kval" -v b="$_got" 'BEGIN {
			if (a == b) {print 1; exit}
			if (a + 0 != 0 && b + 0 != 0 && a + 0 == b + 0) {print 1; exit}
			print 0
		}')
		[ "$_same" = 1 ] || apply_note="${apply_note:+$apply_note;}mismatch:$_k(want=$_kval,got=$_got)"
	done
	[ -z "$dial_keys" ] || apply_note="${apply_note:+$apply_note;}dialtime:$dial_keys"
	[ "$exit_ok" = 1 ] || apply_note="${apply_note:+$apply_note;}exit=unreachable"
	return 0
}

# --- the session ---------------------------------------------------------------
mux_info() { "$CLI" cli proxy mux info -n "$app" --json 2>/dev/null; }
tp_counters() {
	"$CLI" cli visor state --select transports --json 2>/dev/null |
		jq -r --arg id "$1" '.transports[] | select(.id == $id) | "\(.log.sent) \(.log.recv)"'
}
warm() {
	[ "$SWEEP_WARM" = 1 ] || return 0
	_wbad=0
	for _ in 1 2 3 4; do
		_wc=$(curl -s -m 30 --socks5-hostname "$socks" -o /dev/null -w '%{http_code}' "$sink/?bytes=100000")
		[ "$_wc" = 200 ] || _wbad=$((_wbad + 1))
	done
	echo "$app warm: $((4 - _wbad))/4 probes ok"
	[ "$_wbad" -lt 4 ]
}
res_set() { [ "$EXIT_RES" = 1 ] && "$here/exit-resources.sh" "$out" "$1" "$exit_pk"; return 0; }

# --- rows ----------------------------------------------------------------------
rows="$sw/rows.tsv"
{
	printf '# one row per transfer, in the order they ran. A and B rows of one pair share the pair id.\n'
	printf '# variant\tcell\ttrial\tpair\tMBps\thash_ok\tknobs_read_back\tnote\n'
} > "$rows"
while read -r v; do
	[ -n "$v" ] || continue
	printf '# %s: %s — one deploy, one session, knobs applied between rows\n' "$v" "$(cat "$tmp/knobs.$v")" > "$out/$v.tsv"
	printf '# row\ttp\tsent_delta\trecv_delta\n' > "$out/$v.carrier.tsv"
	echo 0 > "$tmp/rowno.$v"
done < "$tmp/names"

# one_row <variant> <bytes> <dir> <trial> <pair>
one_row() {
	_v=$1; _n=$2; _d=$3; _t=$4; _p=$5
	apply "$_v"
	_r=$(( $(cat "$tmp/rowno.$_v") + 1 )); echo "$_r" > "$tmp/rowno.$_v"
	_tps=$(mux_info | jq -r '.[]?.legs[]?.transport_id' 2>/dev/null | sort -u)
	_before=""
	for _tp in $_tps; do _before="$_before $_tp:$(tp_counters "$_tp" | tr ' ' ',')"; done
	BENCH_FAIL_PREFIX="$out/$_v.row$_r" "$BENCH" "$socks" "$sink" "$_n" "$_d" "$_v-c$_p" >> "$out/$_v.tsv"
	_last=$(tail -1 "$out/$_v.tsv")
	for _tp in $_tps; do
		_b=$(echo "$_before" | tr ' ' '\n' | grep "^$_tp:" | cut -d: -f2)
		_a=$(tp_counters "$_tp" | tr ' ' ',')
		if [ -n "$_a" ] && [ -n "$_b" ]; then
			printf '%s\t%s\t%s\t%s\n' "$_r" "$_tp" "$(( ${_a%,*} - ${_b%,*} ))" "$(( ${_a#*,} - ${_b#*,} ))" >> "$out/$_v.carrier.tsv"
		else
			printf '%s\t%s\t?\t?\n' "$_r" "$_tp" >> "$out/$_v.carrier.tsv"
		fi
	done
	printf '%s\tlegs\t%s\t-\n' "$_r" "$(mux_info | jq -r '[.[] | (.desc.dst_port | tostring) + ":" + ([.legs[].transport_id[0:8]] | join(","))] | join(" ")' 2>/dev/null)" >> "$out/$_v.carrier.tsv"
	_mbps=$(echo "$_last" | awk -F'\t' '{printf "%.3f", $4 / 1e6}')
	_ok=$(echo "$_last" | cut -f8)
	printf '%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n' \
		"$_v" "$_n_label" "$_t" "$_p" "$_mbps" "$_ok" "$apply_read" "${apply_note:--}" >> "$rows"
	echo "  $_v $_n_label trial $_t pair $_p: $_mbps MB/s hash_ok=$_ok"
	return 0
}

norm_sizes() {
	for _s in $1; do
		case $_s in *[!0-9]* | "") continue ;; esac
		[ "$_s" -lt 1000 ] && _s=$((_s * 1000000))
		echo "$_s"
	done
}
sizes=$(norm_sizes "$sizes" | tr '\n' ' ')

echo "run-variants: A=$a_name  B=$b_list"
echo "run-variants: cells=$(echo "$sizes" | tr ' ' ',')bytes x $dirs, $trials trial(s), bar=$bar, app=$app on $socks, exit=$exit_pk"
[ "$SWEEP_DRY" = 1 ] && echo "run-variants: DRY RUN — stub CLI $CLI, stub bench $BENCH, state $SWEEP_DRY_STATE"
warm || echo "run-variants: the session failed every warm probe — the rows will show it"
res_set sweep-pre

pair=0
for n in $sizes; do
	for d in $dirs; do
		_n_label="$((n / 1000000))$d"
		t=1
		while [ "$t" -le "$trials" ]; do
			for b in $b_list; do
				pair=$((pair + 1))
				one_row "$a_name" "$n" "$d" "$t" "$pair"
				one_row "$b" "$n" "$d" "$t" "$pair"
			done
			t=$((t + 1))
		done
	done
done

res_set sweep-post
if [ "$EXIT_RES" = 1 ] && [ "${EXIT_RES_SETTLE_S:-30}" -gt 0 ] 2>/dev/null; then
	sleep "${EXIT_RES_SETTLE_S:-30}"
	res_set sweep-settled
fi
[ "$EXIT_RES" = 1 ] && { "$here/exit-resources-check.sh" "$out" sweep || exit_res_fail=1; }

# --- the verdict ---------------------------------------------------------------
# median(B)/median(A) per variant and cell, over the A rows B was actually
# interleaved with, plus every per-pair ratio. hash_ok is counted over the
# variant's own rows and wire/goodput is taken from bench/summarize.sh, which
# reads the same <variant>.tsv / <variant>.carrier.tsv pair every other runner
# writes.
"$here/summarize.sh" "$out" > "$sw/summary.txt" 2>/dev/null || : > "$sw/summary.txt"
{
	printf '# verdict.tsv — median(B)/median(A) per variant and cell, A interleaved with B\n'
	printf '# bar=%s (a variant PASSes a cell when its ratio reaches the bar)\n' "$bar"
	printf '# variant\tcell\tratio\tmedian_B\tmedian_A\tpairs\thash_ok\twire/good\tper_pair_ratios\tverdict\n'
	awk -F'\t' -v a="$a_name" -v bar="$bar" -v sumf="$sw/summary.txt" '
		function med(arr, n,   i, j, x) {
			for (i = 2; i <= n; i++) { x = arr[i]; j = i - 1; while (j > 0 && arr[j] > x) { arr[j + 1] = arr[j]; j-- } arr[j + 1] = x }
			return (n % 2) ? arr[(n + 1) / 2] : (arr[n / 2] + arr[n / 2 + 1]) / 2
		}
		BEGIN {
			while ((getline line < sumf) > 0) {
				split(line, f, /[ \t]+/)
				if (f[3] ~ /^[0-9]+$/) wire[f[1] "\t" f[3] f[2]] = f[9]
			}
		}
		/^#/ { next }
		{
			v = $1; cell = $2; pair = $4; r = $5 + 0; ok = $6
			if (v == a) { arate[cell "\t" pair] = r; next }
			key = v "\t" cell
			if (!(key in seen)) { seen[key] = 1; order[++norder] = key }
			nb[key]++; brates[key, nb[key]] = r
			bpair[key, nb[key]] = pair
			hok[key] += (ok == 1)
			hn[key]++
		}
		END {
			for (i = 1; i <= norder; i++) {
				key = order[i]; split(key, kf, "\t"); v = kf[1]; cell = kf[2]
				n = nb[key]; ratios = ""; na = 0
				split("", bv); split("", av)
				for (j = 1; j <= n; j++) {
					bv[j] = brates[key, j]
					p = bpair[key, j]
					ar = arate[cell "\t" p]
					if (ar > 0) {
						av[++na] = ar
						ratios = ratios (ratios ? "," : "") sprintf("%.3f", bv[j] / ar)
					} else {
						ratios = ratios (ratios ? "," : "") "-"
					}
				}
				if (n == 0 || na == 0) continue
				mb = med(bv, n); ma = med(av, na)
				ratio = (ma > 0) ? mb / ma : 0
				verdict = (ratio + 0 >= bar + 0) ? "PASS" : "FAIL"
				w = wire[v "\t" cell]; if (w == "") w = "-"
				printf "%s\t%s\t%.3f\t%.2f\t%.2f\t%d\t%d/%d\t%s\t%s\t%s\n", v, cell, ratio, mb, ma, n, hok[key], hn[key], w, ratios, verdict
			}
		}' "$rows"
} > "$sw/verdict.tsv"
restore_knobs
echo "--- $sw/verdict.tsv"
cat "$sw/verdict.tsv"
echo "--- restore"
cat "$restore_file"
[ "$exit_res_fail" = 0 ] || { echo "run-variants: the exit-resource gate FAILED — results are on disk"; exit 1; }
exit 0
