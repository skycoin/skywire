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
# Functions are copied from run-mux.sh rather than sourced: it is a script with
# top-level work, not a library.
set -u
CLI=${CLI:-/home/d0mo/go/bin/skywire}
exit_pk=$1; out=$2; pins=$3; trials=${4:-5}; sink=${5:-http://127.0.0.1:18080}
order=${6:-$(ls "$pins"/via-*.json | sed 's|.*/via-||; s|\.json$||' | tr '\n' ' ')}
here=$(dirname "$0")
mkdir -p "$out"
local_commit=$(git -C "$here/.." rev-parse --short=9 HEAD)
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
tp_present() { # <tp id> -> 0 when the local visor holds it
	$CLI cli tp ls --json 2>/dev/null |
		jq -e --arg id "$1" 'any(.[]; .id==$id)' >/dev/null 2>&1
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
now() { date +%s.%N; }
since_s() { awk -v a="$1" -v b="$(now)" 'BEGIN{printf "%.3f", b - a}'; }
ge() { awk -v a="$1" -v b="$2" 'BEGIN{exit !(a + 0 >= b + 0)}'; }
div() { awk -v a="$1" -v b="$2" 'BEGIN{if (b + 0 > 0) printf "%.0f", a / b; else print "-"}'; }
# pin_short <tp id>: the pin file whose FIRST HOP is that transport. Doubles as
# the "is this one of the six pins" fence — a transport no pin names cannot be
# restored to the same id and is never cut.
pin_short() {
	for p in "$pins"/via-*.json; do
		[ "$(jq -r '.[0].forward[0].TpID' "$p" 2>/dev/null)" = "$1" ] && { basename "$p" .json | sed 's/^via-//'; return 0; }
	done
	return 1
}
pin_pk() { jq -r '.[0].forward[0].To' "$pins/via-$1.json" 2>/dev/null; }

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

# do_cut: the one mid-transfer operation, by subject. Sets cut_ok.
do_cut() {
	case $cut_kind in
	leg)
		timeout 60 $CLI cli proxy mux rm "$cut_tp" -n "$name" --rg "$cut_rg" >> "$out/$set_name.cut.log" 2>&1 && cut_ok=1 || cut_ok=0
		;;
	tp)
		timeout 60 $CLI cli tp rm "$cut_tp" >> "$out/$set_name.cut.log" 2>&1 && cut_ok=1 || cut_ok=0
		;;
	*) cut_ok=0 ;;
	esac
}

# restore_cut: put the rig back. Echoes 1 when the leg/transport is present again.
restore_cut() {
	case $cut_kind in
	leg)
		# the group may have been rebuilt on a new port while the row ran; add the
		# leg back to whatever group the session holds now.
		_rg=$(mux_info "$name" | jq -r '.[0].desc.dst_port // empty')
		timeout 180 $CLI cli proxy mux add -n "$name" ${_rg:+--rg $_rg} --route "$pins/via-$cut_short.json" >> "$out/$set_name.cut.log" 2>&1
		sleep 2
		if mux_info "$name" | jq -e --arg id "$cut_tp" 'any(.[].legs[]; .transport_id==$id)' >/dev/null 2>&1; then echo 1; else echo 0; fi
		;;
	tp)
		# a re-dialled stcpr is not up the instant `tp add` returns, and a row that
		# starts without it strands the next re-establish on the direct transport to
		# the exit (which choose_cut then rightly refuses) — so retry until it is
		# actually back rather than sampling once.
		_ra=1
		while [ "$_ra" -le 4 ]; do
			tp_present "$cut_tp" && { echo 1; return; }
			timeout 120 $CLI cli tp add -t stcpr "$cut_pk" >> "$out/$set_name.cut.log" 2>&1
			sleep 5
			_ra=$((_ra + 1))
		done
		if tp_present "$cut_tp"; then echo 1; else echo 0; fi
		;;
	*) echo 0 ;;
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
# Appends the bench.sh row to $f and the cut record to $cutf.
cut_row() {
	label=$1; dir=$2
	w="$tmp/row"; rm -rf "$w"; mkdir -p "$w"
	want_bytes=$((size * cut_pct / 100))
	poll=0.25
	if [ "$dir" = up ]; then
		poll=1; tp_sent_all > "$tmp/base" 2>/dev/null; : > "$tmp/last"
	fi
	start=$(now)
	if [ "$dir" = down ]; then
		curl -s --socks5-hostname "$socks" -m 900 -D "$w/h" -o "$w/b" \
			-w '%{http_code} %{size_download} %{time_total} %{speed_download}' "$sink/?bytes=$size" > "$w/w" 2>/dev/null &
	else
		curl -s --socks5-hostname "$socks" -m 900 -o "$w/r" \
			-w '%{http_code} %{size_upload} %{time_total} %{speed_upload}' \
			-X POST --data-binary "@$payload" "$sink/upload" > "$w/w" 2>/dev/null &
	fi
	cpid=$!
	cut_done=0; cut_at=0; bytes_at_cut=0; ttfb=-; cut_ok=0
	while kill -0 "$cpid" 2>/dev/null; do
		e=$(since_s "$start")
		if [ "$dir" = down ]; then
			p=0; [ -f "$w/b" ] && p=$(wc -c < "$w/b" | tr -d ' ')
		else
			p=$(up_progress)
		fi
		case $p in '' | *[!0-9]*) p=0 ;; esac
		if [ "$cut_done" -eq 0 ]; then
			if [ "$p" -ge "$want_bytes" ] || ge "$e" "$cut_after"; then
				bytes_at_cut=$p; cut_at=$e
				do_cut
				cut_done=1
				echo "$set_name $label: cut $cut_kind $cut_tp at ${cut_at}s after $bytes_at_cut bytes (ok=$cut_ok)"
			fi
		elif [ "$ttfb" = - ] && [ "$p" -gt "$bytes_at_cut" ]; then
			ttfb=$(awk -v a="$e" -v b="$cut_at" 'BEGIN{printf "%.3f", a - b}')
		fi
		sleep "$poll"
	done
	wait "$cpid" 2>/dev/null
	# shellcheck disable=SC2046 # the four -w fields are split on purpose, as bench.sh does
	set -- $(cat "$w/w" 2>/dev/null)
	http=${1:-000}; got=${2:-0}; secs=${3:-0}; speed=${4:-0}
	if [ "$dir" = down ]; then
		want=$(tr -d '\r' < "$w/h" 2>/dev/null | awk 'tolower($1)=="x-sha256:"{print $2}')
		have=$(sha256sum "$w/b" 2>/dev/null | cut -d' ' -f1)
	else
		want=$(jq -r .sha256 "$w/r" 2>/dev/null)
		have=$payload_sha
	fi
	ok=0; [ -n "$want" ] && [ "$want" = "$have" ] && ok=1
	printf '%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n' "$label" "$dir" "$size" "$speed" "$http" "$got" "$secs" "$ok" >> "$f"
	# An upload's progress is wire bytes, a download's is payload bytes, so an
	# upload row's before/after pair is the wire-side approximation (clamped so
	# the remainder can never go negative).
	rem=$((got - bytes_at_cut)); [ "$rem" -lt 0 ] && rem=0
	gp_before=$(div "$bytes_at_cut" "$cut_at")
	gp_after=$(div "$rem" "$(awk -v a="$secs" -v b="$cut_at" 'BEGIN{printf "%.3f", a - b}')")
	restored=$(restore_cut)
	rg_after=$(mux_info "$name" | jq -r '[.[].desc.dst_port] | join(",")' 2>/dev/null)
	printf '%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n' \
		"$row" "$set_name" "$cut_at" "$cut_kind:$cut_tp(ok=$cut_ok)" "$bytes_at_cut" "$ttfb" "$gp_before" "$gp_after" "$restored" "$rg_after" >> "$cutf"
	echo "$set_name $label: http=$http got=$got hash_ok=$ok before=${gp_before}B/s after=${gp_after}B/s ttfb_after=${ttfb}s restored=$restored rg_after=$rg_after"
}

# choose_cut <subject>: pick the leg (and its pin / remote pk) this subject cuts.
# Never the transport to the exit — the rig's direct reference rides it — never
# a transport outside the six pins, and never one a second route group shares.
choose_cut() {
	info=$(mux_info "$name")
	cut_rg=""; cut_tp=""; cut_pk=""; cut_short=""
	case $1 in
	legs-2)
		cut_kind=leg
		cut_rg=$(echo "$info" | jq -r '.[0].desc.dst_port // empty')
		cut_tp=$(echo "$info" | jq -r '.[0].legs[1].transport_id // empty')
		cut_pk=$(echo "$info" | jq -r '.[0].legs[1].remote_pk // empty')
		;;
	tunnels-2)
		cut_kind=tp
		# every group but the first is a candidate; take the first candidate whose
		# hop-1 transport is a pin and is not shared with another group.
		# sh has no function locals: these loop vars are _c-prefixed so they cannot
		# clobber the caller's trial counter when a re-establish re-selects.
		_cn=$(echo "$info" | jq 'length' 2>/dev/null || echo 0)
		_ci=1
		while [ "$_ci" -lt "$_cn" ]; do
			_ct=$(echo "$info" | jq -r --argjson i "$_ci" '.[$i].legs[0].transport_id // empty')
			_cshared=$(echo "$info" | jq -r --argjson i "$_ci" '[to_entries[] | select(.key != $i) | .value.legs[].transport_id] | join(" ")')
			if [ -n "$_ct" ] && ! echo " $_cshared " | grep -q " $_ct "; then
				cut_tp=$_ct
				cut_rg=$(echo "$info" | jq -r --argjson i "$_ci" '.[$i].desc.dst_port // empty')
				cut_pk=$(echo "$info" | jq -r --argjson i "$_ci" '.[$i].legs[0].remote_pk // empty')
				break
			fi
			[ -n "$_ct" ] && echo "$set_name: rg $(echo "$info" | jq -r --argjson i "$_ci" '.[$i].desc.dst_port') rides $_ct, which another group also holds — not a cut target"
			_ci=$((_ci + 1))
		done
		;;
	esac
	[ -n "$cut_tp" ] || { echo "$set_name: no second leg/tunnel to cut — skipping the subject"; return 1; }
	[ "$cut_pk" != "$exit_pk" ] || { echo "$set_name: the target rides the transport to the EXIT ($cut_tp) — refusing to cut it"; return 1; }
	cut_short=$(pin_short "$cut_tp" || true)
	if [ -z "${cut_short:-}" ]; then
		echo "$set_name: $cut_kind $cut_tp is not one of the $(ls "$pins"/via-*.json | wc -l) pinned hop-1 transports — it could not be restored, skipping the subject"
		return 1
	fi
	# restore by the PIN's pk, not mux info's: `tp add -t stcpr <pk>` has to
	# rebuild the exact id the pin names.
	cut_pk=$(pin_pk "$cut_short")
	echo "$set_name: cut target = $cut_kind $cut_tp on rg $cut_rg (remote $cut_pk, pin via-$cut_short)"
	return 0
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

	echo "# exit=$exit_pk local=$local_commit exit_commit=$ec session=$name subject=$subject $setup route_groups=$groups legs_per_rg=$shape rg_ports=$ports_before groups=$desc cut=$cut_kind:$cut_tp cut_pct=$cut_pct cut_after=${cut_after}s sink=$sink" > "$f"
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
	mux_events "$set_name" "$name"
	ports=$(mux_info "$name" | jq -c '[.[].desc.dst_port]')
	x=""; for _ in 1 2 3; do
		x=$(timeout 90 $CLI cli visor state --via "dmsg://$exit_pk" --select mux_route_groups --json 2>/dev/null | jq -c --argjson p "$ports" '[.mux_route_groups[]? | select(.desc.src_port as $s | $p | index($s)) | {rg: .desc.src_port, recovery, legs: [.legs[] | {tp: .transport_id[0:8], standby, retransmits, dup_bytes, ack_delay_ms}]}]')
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
