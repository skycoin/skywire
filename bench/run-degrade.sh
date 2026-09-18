#!/bin/sh
# run-degrade.sh — criterion 6 "degradation": cut a leg mid-transfer, see what
# the session does with the rest of the transfer.
#
#   bench/run-degrade.sh <exit pk> <out dir> <pins dir> [trials] [sink] [pin order]
#
# Subjects (SUBJECTS, default "legs-2 tunnels-2"), one 50 MB transfer per row:
#   legs-2      one route group with 2 pinned two-hop legs (packet-level mux).
#               The cut is `proxy mux rm --rg <dst_port> <tp-id>` on the SECOND
#               leg — a CLI operation on the session, the rig's transports
#               untouched.
#   tunnels-2   `proxy start --tunnels 2`, then every route group pinned to ONE
#               pin of its own by its own port (`mux set --rg <dst_port>`), so
#               the two tunnels are transport-disjoint and both ride a KNOWN
#               hop-1 stcpr from the pin set. `proxy mux rm` cannot be aimed at
#               a whole tunnel, so the cut is `tp rm <id>` on that second
#               tunnel's hop-1 stcpr transport. Transport ids are deterministic
#               (MakeTransportID over the sorted edge keys + type), so re-adding
#               it with `tp add -t stcpr <pk>` restores the SAME id and the pin
#               files stay valid.
#
# The rg selector everywhere is the group's OWN port, desc.dst_port (#4967):
# every rg of one app shares src_port, so --rg <dst_port> is what names one
# group. Passing it even when there is a single group keeps the cut deterministic
# if a stray SOCKS rg appears.
#
# The cut fires when the transfer has moved CUT_PCT (default 40) percent, or
# after CUT_AFTER_S (default 3) seconds, whichever comes first — 3 s because a
# 50 MB upload finishes in ~5.6 s on this rig and a 6 s timer let two upload rows
# run to completion UNCUT. Progress is the
# output file for a download and the carrier transports' sent counters for an
# upload (wire bytes — the only progress an in-flight POST exposes, since
# `--data-binary @file` is buffered in curl and its read offset races ahead), so
# an upload row polls the visor once a second while it runs. The per-carrier
# baselines are kept SEPARATELY: a carrier the cut removes vanishes from the
# visor's transport list, and a single summed baseline would make progress
# collapse by that transport's LIFETIME total and the post-cut ttfb could then
# never fire.
#
# What may be cut is fenced twice: the target transport must be one of the six
# pinned hop-1 stcpr transports (so `tp add -t stcpr <pk>` can restore the exact
# id), it must not be the transport to the EXIT, and for tunnels-2 it must not
# also carry another route group.
#
# Artefacts: mux-degrade-<subject>.tsv (bench.sh's columns), .carrier.tsv,
# .recovery.tsv, .mux_events.json, .legs.json — so summarize.sh and verdict.sh
# work unchanged — plus .cut.tsv:
#   row  subject  cut_at_s  what_cut  bytes_before_cut  ttfb_after_s
#        goodput_before_Bps  goodput_after_Bps  restored  rg_ports_after
# rg_ports_after is the rebuild tell: a group that kept its port continued, a
# port that changed means the session built a new group.
#
# The rig is put back after every row (leg re-added from its pin, transport
# re-added with `tp add -t stcpr <pk>`) and swept once more at the end.
#
# The cut itself — choosing a target, the fences, the cut, the restore and the
# timed transfer it happens inside — now lives in bench/lib-cut.sh, which this
# script sources and run-mux.sh's single CUT_ROW uses too. The rest of the
# helpers are still copied from run-mux.sh: it is a script with top-level work,
# not a library.
# SETTINGS="key=value ..." applies live `proxy settings` knobs to the app under
# test once per set — after it is up and warm, before the first row, waited for
# and recorded in the set header and <set>.settings.json. ROUTE_SETTINGS="--flag
# value ..." does the same for the visor-wide `route settings` and is restored
# at set end. The paired references never receive either. bench/lib-settings.sh;
# bench/run-sweep.sh drives one knob across a list of values.
set -u
CLI=${CLI:-/home/d0mo/go/bin/skywire}
exit_pk=$1; out=$2; pins=$3; trials=${4:-5}; sink=${5:-http://127.0.0.1:18080}
order=${6:-$(ls "$pins"/via-*.json | sed 's|.*/via-||; s|\.json$||' | tr '\n' ' ')}
here=$(dirname "$0")
mkdir -p "$out"
local_commit=$(git -C "$here/.." rev-parse --short=9 HEAD)
CUT_HERE=$here # read by the library below
# Every subject here is PINNED, so this script keeps the strict fence it always
# had: a target that is not one of the pinned hop-1 transports is not cut, and
# the subject is skipped. (run-mux.sh's single cut row uses CUT_FENCE=auto,
# which also allows an auto-dialed tunnel's first hop.)
CUT_FENCE=${CUT_FENCE:-pins}
export CUT_HERE CUT_FENCE
# shellcheck source=bench/lib-cut.sh
. "$here/lib-cut.sh"
# shellcheck source=bench/lib-settings.sh
. "$here/lib-settings.sh"
subjects=${SUBJECTS:-"legs-2 tunnels-2"}
size=${DEGRADE_SIZE:-50000000}
dirs=${DIRS:-"down up"}
cut_pct=${CUT_PCT:-40}
cut_after=${CUT_AFTER_S:-3}
app_name() { echo "${APP:-$1}"; }
app_addr() { if [ -n "${APP:-}" ]; then echo "127.0.0.1:${APP_PORT:-1080}"; else echo "127.0.0.1:$1"; fi; }

tmp=$(mktemp -d); trap 'rm -rf "$tmp"' EXIT INT TERM

tp_counters() {
	$CLI cli visor state --select transports --json 2>/dev/null |
		jq -r --arg id "$1" '.transports[] | select(.id==$id) | "\(.log.sent) \(.log.recv)"'
}
# tp_sent_all: "<id> <sent>" for every carrier of the set that still EXISTS, in
# one RPC. A removed transport is simply absent — up_progress handles that.
tp_sent_all() {
	$CLI cli visor state --select transports --json 2>/dev/null |
		jq -r --arg ids "$tps" '($ids | split(" ") | map(select(. != ""))) as $w |
			.transports[] | select(.id as $i | $w | index($i)) | "\(.id) \(.log.sent)"'
}
exit_commit() {
	timeout 60 $CLI cli visor state --via "dmsg://$exit_pk" --select summary --json 2>/dev/null |
		jq -r '.summary.overview.build_info.commit[0:9] // "unknown"'
}
stop_app() { $CLI cli proxy stop -n "$1" >/dev/null 2>&1; }
# tp id -> remote public IP, as run-mux.sh does: `mux info` names a leg's
# transport but not where it lands. Empty object on any failure.
tp_ips() { $CLI cli tp ls --json 2>/dev/null | jq -c 'map({(.id): (.remote_ip // "")}) | add // {}' 2>/dev/null; }
mux_info() {
	_mi=$($CLI cli proxy mux info -n "$1" --json 2>/dev/null) || return 1
	_mi_ips=$(tp_ips)
	[ -n "$_mi_ips" ] || _mi_ips='{}'
	echo "$_mi" | jq --argjson ips "$_mi_ips" \
		'map(if (.legs | type) == "array"
			then .legs |= map(. + {remote_ip: ($ips[.transport_id // ""] // "")})
			else . end)' 2>/dev/null || echo "$_mi"
}
warm() {
	socks=$1; wname=$2
	bad=0
	for _ in 1 2 3 4; do
		code=$(curl -s -m 30 --socks5-hostname "$socks" -o /dev/null -w '%{http_code}' "$sink/?bytes=100000")
		[ "$code" = 200 ] || bad=$((bad + 1))
	done
	echo "$wname warm: $((4 - bad))/4 probes ok"
	[ $bad -eq 0 ]
}
mux_events() {
	$CLI cli visor state --select diag --json 2>/dev/null |
		jq --arg app "$2" --arg since "${setup_started:-$set_started}" '[.diag.mux_events[]? | select(.app==$app and .at[0:19] >= $since)]' > "$out/$1.mux_events.json" 2>/dev/null
	echo "$1: $(jq 'length' "$out/$1.mux_events.json" 2>/dev/null || echo 0) mux events: $(jq -r '[.[] | .event + "(" + (.reason // "") + ")"] | join(" ")' "$out/$1.mux_events.json" 2>/dev/null | cut -c1-300)"
}
# now / since_s / ge / div / tp_present / pin_short / pin_pk / choose_cut /
# do_cut / restore_cut / cut_transfer all come from bench/lib-cut.sh.

# start_subject <subject>: bring the session up in the shape the subject needs.
# Also used to re-establish it when a cut cost the session a whole tunnel.
start_subject() {
	stop_app "$name"
	case $1 in
	legs-2)
		chosen=$(echo "$order" | tr ' ' '\n' | grep -v '^$' | head -n 2)
		first=$(echo "$chosen" | head -1)
		legs_file="$out/$set_name.target.json"
		for s in $chosen; do cat "$pins/via-$s.json"; done | jq -s 'add' > "$legs_file"
		timeout 240 $CLI cli proxy start -k "$exit_pk" -n "$name" -a "$socks" --route "$pins/via-$first.json" 2>&1 | grep -iv debug | grep -i "pinned\|running\|error\|fatal" | head -3
		timeout 300 $CLI cli proxy mux set -n "$name" --legs "$legs_file" --prune 2>&1 | grep -iv debug | head -5
		$CLI cli proxy mux cap 2 >/dev/null 2>&1; $CLI cli proxy mux width 2 >/dev/null 2>&1
		setup="legs=2 pins=$(echo $chosen | tr ' ' ',')"
		;;
	tunnels-2)
		# one leg per tunnel: cap/width 1 so the adaptive engine cannot grow a
		# second leg into a group and blunt the cut.
		$CLI cli proxy mux cap 1 >/dev/null 2>&1; $CLI cli proxy mux width 1 >/dev/null 2>&1
		timeout 240 $CLI cli proxy start -k "$exit_pk" -n "$name" -a "$socks" --tunnels 2 ${RANGE_PORT:+--range-port $RANGE_PORT} ${RANGE_CHUNK_KIB:+--range-chunk-kib $RANGE_CHUNK_KIB} 2>&1 | grep -iv debug | grep -i "tunnel\|running\|error\|fatal" | head -3
		sleep 5
		# pin each tunnel to ONE pin of its own, by the group's own port (#4967),
		# so both tunnels ride known hop-1 stcpr transports and the cut target is
		# restorable to the same transport id.
		chosen=$(echo "$order" | tr ' ' '\n' | grep -v '^$' | head -n 2)
		i=1; assign=""
		for dp in $(mux_info "$name" | jq -r '.[].desc.dst_port' 2>/dev/null); do
			s=$(echo "$chosen" | sed -n "${i}p")
			[ -n "$s" ] || break
			cp "$pins/via-$s.json" "$out/$set_name.rg$dp.target.json"
			timeout 300 $CLI cli proxy mux set -n "$name" --rg "$dp" --legs "$out/$set_name.rg$dp.target.json" --prune 2>&1 | grep -iv debug | head -3
			assign="$assign rg$dp=$s"; i=$((i + 1))
		done
		$CLI cli proxy mux cap 1 >/dev/null 2>&1; $CLI cli proxy mux width 1 >/dev/null 2>&1
		setup="tunnels=2 pin_assign=$(echo $assign)"
		;;
	esac
}

# up_progress: wire bytes this row has pushed, summed per carrier against a
# PER-CARRIER baseline; a carrier the cut removed keeps its last delta instead
# of dropping out of the sum.
up_progress() {
	tp_sent_all > "$tmp/now" 2>/dev/null
	awk -v basef="$tmp/base" -v lastf="$tmp/last" '
		BEGIN { while ((getline l < basef) > 0) { split(l, a, " "); b[a[1]] = a[2] }
			while ((getline l < lastf) > 0) { split(l, a, " "); v[a[1]] = a[2] } }
		{ if ($1 in b) { d = $2 - b[$1]; if (d < 0) d = 0; v[$1] = d } }
		END { s = 0; for (k in v) { s += v[k]; print k, v[k] > (lastf ".new") }
			printf "%d\n", s }
	' "$tmp/now" 2>/dev/null
	[ -f "$tmp/last.new" ] && mv "$tmp/last.new" "$tmp/last"
}

# cut_row <label> <dir>: one hash-verified transfer with a cut in the middle.
# The transfer, the cut and its timing are cut_transfer's (bench/lib-cut.sh);
# this writes the two rows the set records — the bench.sh row to $f and the cut
# record to $cutf — and puts the rig back.
# shellcheck disable=SC2154,SC2034 # ct_* and cut_* cross the lib-cut.sh source boundary
cut_row() {
	label=$1; dir=$2
	ct_payload=$payload; ct_payload_sha=$payload_sha
	cut_transfer "$label" "$dir" "$size"
	printf '%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n' "$label" "$dir" "$size" "$ct_speed" "$ct_http" "$ct_got" "$ct_secs" "$ct_ok" >> "$f"
	# An upload's progress is wire bytes, a download's is payload bytes, so an
	# upload row's before/after pair is the wire-side approximation (clamped so
	# the remainder can never go negative).
	rem=$((ct_got - ct_bytes_at_cut)); [ "$rem" -lt 0 ] && rem=0
	gp_before=$(div "$ct_bytes_at_cut" "$ct_cut_at")
	gp_after=$(div "$rem" "$(awk -v a="$ct_secs" -v b="$ct_cut_at" 'BEGIN{printf "%.3f", a - b}')")
	restored=$(restore_cut)
	rg_after=$(mux_info "$name" | jq -r '[.[].desc.dst_port] | join(",")' 2>/dev/null)
	printf '%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n' \
		"$row" "$set_name" "$ct_cut_at" "$cut_kind:$cut_tp(ok=$ct_cut_ok)" "$ct_bytes_at_cut" "$ct_ttfb" "$gp_before" "$gp_after" "$restored" "$rg_after" >> "$cutf"
	echo "$set_name $label: http=$ct_http got=$ct_got hash_ok=$ct_ok before=${gp_before}B/s after=${gp_after}B/s ttfb_after=${ct_ttfb}s restored=$restored rg_after=$rg_after"
}

ec=$(exit_commit)
echo "local=$local_commit exit=$ec order=$order subjects=$subjects size=$size cut_pct=$cut_pct cut_after=${cut_after}s"
payload="$tmp/payload"
head -c "$size" /dev/urandom > "$payload"
payload_sha=$(sha256sum "$payload" | cut -d' ' -f1)

port=1131
for subject in $subjects; do
	set_name="mux-degrade-$subject"
	name=$(app_name "deg$subject"); socks=$(app_addr "$port")
	setup_started=$(date +%Y-%m-%dT%H:%M:%S)
	f="$out/$set_name.tsv"; c="$out/$set_name.carrier.tsv"; cutf="$out/$set_name.cut.tsv"
	stop_app "$name"

	case $subject in
	legs-2 | tunnels-2)
		[ "$(echo "$order" | tr ' ' '\n' | grep -vc '^$')" -ge 2 ] || { echo "$set_name: need 2 pins — skipping"; continue; }
		;;
	*) echo "unknown subject '$subject' (want legs-2 | tunnels-2)"; continue ;;
	esac
	setup=""
	rm -f "$out/$set_name".rg*.target.json
	start_subject "$subject"
	sleep 5
	mux_info "$name" > "$out/$set_name.legs.json"
	groups=$(jq 'length' "$out/$set_name.legs.json" 2>/dev/null || echo 0)
	shape=$(jq -r '[.[].legs | length] | join("+")' "$out/$set_name.legs.json" 2>/dev/null)
	tps=$(jq -r '.[].legs[].transport_id' "$out/$set_name.legs.json" 2>/dev/null | sort -u | tr '\n' ' ')
	ports_before=$(jq -r '[.[].desc.dst_port] | join(",")' "$out/$set_name.legs.json" 2>/dev/null)
	desc=$(jq -r '[.[] | "rg\(.desc.dst_port)=[" + ([.legs[] | .tp_type + ">" + .remote_pk[0:8] + "@" + .transport_id[0:8]] | join(";")) + "]"] | join(" ")' "$out/$set_name.legs.json" 2>/dev/null)
	echo "$name: $groups route group(s), legs per rg: $shape; tps: $tps"
	want_groups=1; [ "$subject" = tunnels-2 ] && want_groups=2
	[ "$groups" -eq "$want_groups" ] || echo "$name: WARNING $groups route group(s), wanted $want_groups — set recorded as-is"
	choose_cut "$subject" || { stop_app "$name"; port=$((port + 1)); continue; }
	warm "$socks" "$name" || echo "$name: probes failing — running the set anyway"
	# live knobs, once per set: the visor's app store is cleared when the app
	# stops, so SETTINGS can only be applied here — after the dial, the shape
	# check and the warm probes, and before the first row (bench/lib-settings.sh).
	settings_apply "$set_name" "$name"

	echo "# exit=$exit_pk local=$local_commit exit_commit=$ec session=$name subject=$subject $setup route_groups=$groups legs_per_rg=$shape rg_ports=$ports_before groups=$desc cut=$cut_kind:$cut_tp cut_pct=$cut_pct cut_after=${cut_after}s sink=$sink${settings_note:+ $settings_note}" > "$f"
	printf '# row\ttp\tsent_delta\trecv_delta\n' > "$c"
	printf '# row\tsubject\tcut_at_s\twhat_cut\tbytes_before_cut\tttfb_after_s\tgoodput_before_Bps\tgoodput_after_Bps\trestored\trg_ports_after\n' > "$cutf"
	: > "$out/$set_name.cut.log"
	: > "$out/$set_name.recovery.tsv"
	set_started=$(date +%Y-%m-%dT%H:%M:%S)
	row=0
	for dir in $dirs; do
		t=1
		while [ $t -le "$trials" ]; do
			row=$((row + 1))
			before=""
			for tp in $tps; do before="$before $tp:$(tp_counters "$tp" | tr ' ' ',')"; done
			cut_row "$set_name-t$t" "$dir"
			for tp in $tps; do
				b=$(echo "$before" | tr ' ' '\n' | grep "^$tp:" | cut -d: -f2)
				a=$(tp_counters "$tp" | tr ' ' ',')
				[ -n "$a" ] && [ -n "$b" ] || { printf '%s\t%s\t?\t?\n' "$row" "$tp" >> "$c"; continue; }
				printf '%s\t%s\t%s\t%s\n' "$row" "$tp" "$(( ${a%,*} - ${b%,*} ))" "$(( ${a#*,} - ${b#*,} ))" >> "$c"
			done
			info=$(mux_info "$name")
			printf '%s\tlegs\t%s\t-\n' "$row" "$(echo "$info" | jq -r '[.[] | (.desc.dst_port|tostring) + ":" + ([.legs[].transport_id[0:8]] | join(",")) ] | join(" ")')" >> "$c"
			printf '%s\t%s\n' "$row" "$(echo "$info" | jq -c '[.[] | {rg: .desc.dst_port, recovery}]')" >> "$out/$set_name.recovery.tsv"
			# the session has to be back in its shape before the next row, or the
			# next row measures an already-degraded session
			if [ "$cut_kind" = leg ] && ! echo "$info" | jq -e --arg id "$cut_tp" 'any(.[].legs[]; .transport_id==$id)' >/dev/null 2>&1; then
				echo "$set_name: leg $cut_tp still missing after restore — re-reconciling the leg set"
				timeout 300 $CLI cli proxy mux set -n "$name" --legs "$out/$set_name.target.json" 2>&1 | grep -iv debug | head -3
				sleep 2
				info=$(mux_info "$name")
			fi
			# Re-establish when the session is not in the shape the subject needs:
			# too few groups, or — the case that silently voided rows 2-6 of the
			# first tunnels-2 run — the cut target is no longer a leg anywhere.
			# A `tp rm` kills the tunnel that rode it; the app re-dials a FRESH
			# group on whatever first hop it likes (it picked the direct transport
			# to the exit), and every later row then "cut" a transport no tunnel
			# was using. Membership, not group count, is the condition.
			held=$(echo "$info" | jq -r '[.[].legs[].transport_id] | join(" ")' 2>/dev/null)
			if [ "$(echo "$info" | jq 'length' 2>/dev/null || echo 0)" -lt "$want_groups" ] ||
				! echo " $held " | grep -q " $cut_tp "; then
				echo "$set_name: session is not in shape after the cut (groups=$(echo "$info" | jq 'length'), cut target $cut_tp held=$(echo " $held " | grep -qc " $cut_tp " || echo no)) — re-establishing before the next row"
				start_subject "$subject"
				sleep 5
				choose_cut "$subject" || { echo "$set_name: no cut target after re-establish — ending the set"; break 2; }
				tps=$(mux_info "$name" | jq -r '.[].legs[].transport_id' | sort -u | tr '\n' ' ')
			fi
			t=$((t + 1))
		done
	done
	echo "$set_name: $(grep -vc '^#' "$f") rows, hash_ok=$(grep -v '^#' "$f" | awk -F'\t' '$8==1' | wc -l), cuts=$(grep -vc '^#' "$cutf")"
	settings_restore "$set_name" # the app knobs die with the app; the ROUTER knobs do not
	mux_events "$set_name" "$name"
	ports=$(mux_info "$name" | jq -c '[.[].desc.dst_port]')
	x=""; for _ in 1 2 3; do
		x=$(timeout 90 $CLI cli visor state --via "dmsg://$exit_pk" --select mux_route_groups --json 2>/dev/null | jq -c --argjson p "$ports" '[.mux_route_groups[]? | select(.desc.src_port as $s | $p | index($s)) | {rg: .desc.src_port, recovery, legs: [.legs[] | {tp: .transport_id[0:8], standby, retransmits, dup_bytes, ack_delay_ms, sent_bytes, sent_packets, recv_bytes, recv_packets}]}]')
		[ -n "$x" ] && [ "$x" != "[]" ] && break
		sleep 3
	done
	printf '# exit\t%s\n' "$x" >> "$out/$set_name.recovery.tsv"
	ports_after=$(mux_info "$name" | jq -r '[.[].desc.dst_port] | join(",")')
	printf '# rg dst_ports before=%s after=%s %s\n' "$ports_before" "$ports_after" \
		"$([ "$ports_before" = "$ports_after" ] && echo constant || echo CHANGED)" >> "$c"
	echo "$name: rg dst_ports $ports_before -> $ports_after"
	$CLI cli proxy mux cap 8 >/dev/null 2>&1; $CLI cli proxy mux width 2 >/dev/null 2>&1
	stop_app "$name"
	port=$((port + 1))
done

# --- final rig sweep: every hop-1 pin transport back, as rig-restore.sh leaves it
echo "--- rig sweep"
for p in "$pins"/via-*.json; do
	id=$(jq -r '.[0].forward[0].TpID' "$p"); pk=$(jq -r '.[0].forward[0].To' "$p")
	tp_present "$id" && continue
	echo "re-adding hop-1 stcpr to $pk ($id)"
	timeout 120 $CLI cli tp add -t stcpr "$pk" 2>&1 | grep -v DEBUG | grep -i error
	sleep 1
	tp_present "$id" || echo "WARNING: $id still missing — run rig-restore.sh"
done
echo "hop-1 stcpr present: $(for p in "$pins"/via-*.json; do tp_present "$(jq -r '.[0].forward[0].TpID' "$p")" && echo x; done | wc -l)/$(ls "$pins"/via-*.json | wc -l)"
