#!/bin/sh
# shellcheck disable=SC2154,SC2034 # a library: $out, $set_name, $sink, $pins, $name, $exit_pk and the
# cut_*/ct_*/paired_* results are the CALLER's variables, set and read across the source boundary.
# lib-cut.sh — cut a leg (or a whole tunnel's first hop) mid-transfer, and put
# the rig back afterwards.
#
#   CUT_HERE=$here; . "$here/lib-cut.sh"
#
# A LIBRARY: functions only, no top-level work beyond defaulting its own
# variables. It is the one copy of the cut logic — run-degrade.sh (criterion 6,
# a whole set of cut rows) and run-mux.sh (CUT_ROW, the single cut row the goal
# text asks every campaign to carry) both source it instead of holding their
# own copy, which is how the two drifted apart before.
#
# WHAT MAY BE CUT is fenced, always:
#   - never the transport to the EXIT (the direct reference rides it),
#   - for a tunnel cut, never a first hop a SECOND route group also holds,
#   - and the target must be restorable to the SAME transport id, because the
#     pin files name transport ids. Transport ids are deterministic
#     (MakeTransportID over the sorted edge keys + type), so `tp add -t <type>
#     <pk>` rebuilds the exact id.
# CUT_FENCE picks how strict "restorable" is:
#   pins   only one of the pinned hop-1 transports (run-degrade.sh's rule, and
#          the right one for a pinned set).
#   auto   (default) a pinned hop-1 transport when the set has one, otherwise
#          any first hop whose type and remote pk are known — which is what an
#          AUTO-DIALED set has, and without it the shipping default
#          (`--tunnels 2`, no pins at all) could never carry a cut row.
#
# Cut kinds:
#   leg    `proxy mux rm --rg <dst_port> <tp id>`: drops one leg from a route
#          group. A CLI operation on the session; the rig's transports are
#          untouched. Restored with `proxy mux add --route <pin>`.
#   tp     `tp rm <id>`: removes the first-hop transport under a whole tunnel,
#          because `proxy mux rm` cannot be aimed at a tunnel. Restored with
#          `tp add -t <type> <pk>`.
# CUT_REF_FENCE (1) keeps the cut away from the PAIRED REFERENCE's own route.
# campaign21 cut Atlanta's first hop 95839ad0-b588-0b1d-8475-45fba6ae993c while
# paired-ref.txt named that same route (0371ab4b) for the :1081 reference
# instance, so the reference lost its route at row 11 and rows 12-16 measured
# the mux session against a rebuilt reference — their ratios are not comparable
# to rows 1-10. Excluded from the candidates while it is on:
#   - the first-hop transport of the reference route resolved for slot 1 (the
#     same pin file lib-paired.sh starts the instance on), and
#   - every transport the reference instances hold right now
#     (PAIRED_SLOT1_NAME / PAIRED_SLOT2_NAME route groups).
# When no reference is in play — run-degrade.sh, which sources this library
# without lib-paired.sh — the list resolves empty and choose_cut behaves exactly
# as it did. When it empties the candidates the set carries NO cut row: cutting
# the reference costs more evidence than the cut row buys.
CUT_REF_FENCE=${CUT_REF_FENCE:-1}
CUT_FENCE=${CUT_FENCE:-auto}
CUT_PCT=${CUT_PCT:-40}
CUT_AFTER_S=${CUT_AFTER_S:-3}
# A cut that lands with CUT_LATE_PCT or more of the object already delivered
# measured nothing: no traffic is left for the recovery to carry, so an empty
# ct_ttfb is a property of the cut's timing and not of the router. Such a row
# reports ct_ttfb as `late` — INVALID, not a failure. CUT_POLL is the download
# poll cadence; the progress signal is the size of curl's output file and the
# bytes reach it in bursts, so sampling it coarsely is how a 40 % trigger gets
# missed entirely (bench/run-standby.sh cut_deadline).
CUT_LATE_PCT=${CUT_LATE_PCT:-90}
CUT_POLL=${CUT_POLL:-0.1}
CUT_HERE=${CUT_HERE:-$(dirname "$0")}
CLI=${CLI:-/home/d0mo/go/bin/skywire}
# cut_transfer reads these lowercase names; a caller that sets them itself (as
# run-degrade.sh does, from the same two variables) simply sets them again.
cut_pct=${cut_pct:-$CUT_PCT}
cut_after=${cut_after:-$CUT_AFTER_S}

now() { date +%s.%N; }
since_s() { awk -v a="$1" -v b="$(now)" 'BEGIN{printf "%.3f", b - a}'; }
ge() { awk -v a="$1" -v b="$2" 'BEGIN{exit !(a + 0 >= b + 0)}'; }
div() { awk -v a="$1" -v b="$2" 'BEGIN{if (b + 0 > 0) printf "%.0f", a / b; else print "-"}'; }

tp_present() { # <tp id> -> 0 when the local visor holds it
	$CLI cli tp ls --json 2>/dev/null |
		jq -e --arg id "$1" 'any(.[]; .id==$id)' >/dev/null 2>&1
}
# pin_short <tp id>: the pin file whose FIRST HOP is that transport, and the
# "is this one of the pins" fence in one.
pin_short() {
	for _cp in "$pins"/via-*.json; do
		[ "$(jq -r '.[0].forward[0].TpID' "$_cp" 2>/dev/null)" = "$1" ] && { basename "$_cp" .json | sed 's/^via-//'; return 0; }
	done
	return 1
}
pin_pk() { jq -r '.[0].forward[0].To' "$pins/via-$1.json" 2>/dev/null; }

# up_progress / tp_sent_all: overridden by a caller that cuts UPLOAD rows
# (run-degrade.sh tracks wire bytes per carrier there, because a buffered POST
# exposes no other progress). A caller that only cuts downloads never reaches
# them; these stubs keep cut_transfer honest if one ever does.
up_progress() { echo 0; }
tp_sent_all() { :; }

# --- the paired reference is never a cut target (campaign21) -------------------
# ref_pin_first_hop: the first-hop transport of the paired reference route, or
# empty. The token is the caller's $paired_ref when it has one (run-mux.sh
# resolves it once for the whole run), else paired_resolve's — the pin file is
# resolved exactly as lib-paired.sh resolves it to start the instance. `direct`
# has no pin of its own: it rides the transport to the EXIT, which choose_cut
# already refuses outright.
ref_pin_first_hop() {
	_rt=${paired_ref:-}
	if [ -z "$_rt" ] && command -v paired_resolve >/dev/null 2>&1; then
		_rt=$(paired_resolve "$out" 1 2>/dev/null)
	fi
	case ${_rt:-} in '' | direct) return 0 ;; esac
	_rp=$_rt
	[ -f "$_rp" ] || _rp="$pins/via-$_rt.json"
	[ -f "$_rp" ] || return 0
	jq -r '.[0].forward[0].TpID // empty' "$_rp" 2>/dev/null
}
# ref_held_tps: every transport the two reference instances hold right now. A
# stopped instance answers nothing, which is the run-degrade.sh case.
ref_held_tps() {
	for _rn in "${PAIRED_SLOT1_NAME:-skysocks-client-ref}" "${PAIRED_SLOT2_NAME:-skysocks-client-ref2}"; do
		$CLI cli proxy mux info -n "$_rn" --json 2>/dev/null |
			jq -r '.[]?.legs[]?.transport_id // empty' 2>/dev/null
	done
}
# cut_ref_tps: the fenced list, space separated. Empty with CUT_REF_FENCE=0.
cut_ref_tps() {
	[ "$CUT_REF_FENCE" = 1 ] || return 0
	{ ref_pin_first_hop; ref_held_tps; } | grep -v '^$' | sort -u | tr '\n' ' ' | sed 's/ *$//'
}
# is_ref_tp <tp id>: 0 when that transport belongs to the paired reference.
is_ref_tp() { case " ${ref_tps:-} " in *" $1 "*) return 0 ;; esac; return 1; }

# choose_cut <shape>: pick the leg or tunnel first hop this set cuts, and prove
# it is safe to cut. shape is leg | tp | auto, and the run-degrade.sh subject
# names legs-N / tunnels-N are accepted as the same thing.
# Sets cut_kind cut_rg cut_tp cut_pk cut_short cut_type. Returns 1 when nothing
# may be cut, with the reason in cut_skip_reason — the caller then runs the set
# with no cut rather than cutting something it cannot put back, or the
# reference every row of the set is measured against.
choose_cut() {
	# ACTIVE groups only: a standby tunnel (#4986) is dialed and kept alive but
	# carries no streams, so cutting one would measure nothing. tunnel_role is
	# omitempty — when no group carries it, every group is a candidate, which is
	# the behaviour every binary before the pool had.
	info=$(mux_info "$name" | jq -c '[.[]?] as $rgs | if ($rgs | map(select(.tunnel_role != null)) | length) == 0 then $rgs else ($rgs | map(select(.tunnel_role == "active"))) end' 2>/dev/null)
	cut_rg=""; cut_tp=""; cut_pk=""; cut_short=""; cut_type=stcpr
	cut_skip_reason=""; _cref_blocked=0
	ref_tps=$(cut_ref_tps)
	[ -z "$ref_tps" ] || echo "$set_name: the paired reference holds $(printf '%s' "$ref_tps" | wc -w | tr -d ' ') transport(s), fenced off the cut: $ref_tps"
	_cshape=$1
	case $_cshape in
	legs-*) _cshape=leg ;;
	tunnels-*) _cshape=tp ;;
	auto)
		# one group with several legs is a leg cut; several groups is a tunnel cut.
		if [ "$(echo "$info" | jq 'length' 2>/dev/null || echo 0)" -gt 1 ]; then _cshape=tp; else _cshape=leg; fi
		;;
	esac
	case $_cshape in
	leg)
		cut_kind=leg
		cut_rg=$(echo "$info" | jq -r '.[0].desc.dst_port // empty')
		# every leg but the first is a candidate; take the first that is not the
		# paired reference's.
		_cn=$(echo "$info" | jq '.[0].legs | length' 2>/dev/null)
		case ${_cn:-} in '' | *[!0-9]*) _cn=0 ;; esac
		_ci=1
		while [ "$_ci" -lt "$_cn" ]; do
			_ct=$(echo "$info" | jq -r --argjson i "$_ci" '.[0].legs[$i].transport_id // empty')
			if [ -n "$_ct" ] && ! is_ref_tp "$_ct"; then
				cut_tp=$_ct
				cut_pk=$(echo "$info" | jq -r --argjson i "$_ci" '.[0].legs[$i].remote_pk // empty')
				cut_type=$(echo "$info" | jq -r --argjson i "$_ci" '.[0].legs[$i].tp_type // "stcpr"')
				break
			fi
			if [ -n "$_ct" ]; then
				echo "$set_name: leg $_ct is the paired reference's own route — not a cut target"
				_cref_blocked=1
			fi
			_ci=$((_ci + 1))
		done
		;;
	tp)
		cut_kind=tp
		# every group but the first is a candidate; take the first whose hop-1
		# transport is neither shared with another group nor the reference's.
		_cn=$(echo "$info" | jq 'length' 2>/dev/null || echo 0)
		_ci=1
		while [ "$_ci" -lt "$_cn" ]; do
			_ct=$(echo "$info" | jq -r --argjson i "$_ci" '.[$i].legs[0].transport_id // empty')
			_cshared=$(echo "$info" | jq -r --argjson i "$_ci" '[to_entries[] | select(.key != $i) | .value.legs[].transport_id] | join(" ")')
			if [ -n "$_ct" ] && ! echo " $_cshared " | grep -q " $_ct " && ! is_ref_tp "$_ct"; then
				cut_tp=$_ct
				cut_rg=$(echo "$info" | jq -r --argjson i "$_ci" '.[$i].desc.dst_port // empty')
				cut_pk=$(echo "$info" | jq -r --argjson i "$_ci" '.[$i].legs[0].remote_pk // empty')
				cut_type=$(echo "$info" | jq -r --argjson i "$_ci" '.[$i].legs[0].tp_type // "stcpr"')
				break
			fi
			if [ -n "$_ct" ] && is_ref_tp "$_ct"; then
				echo "$set_name: rg $(echo "$info" | jq -r --argjson i "$_ci" '.[$i].desc.dst_port') rides $_ct, the paired reference's own first hop — not a cut target"
				_cref_blocked=1
			elif [ -n "$_ct" ]; then
				echo "$set_name: rg $(echo "$info" | jq -r --argjson i "$_ci" '.[$i].desc.dst_port') rides $_ct, which another group also holds — not a cut target"
			fi
			_ci=$((_ci + 1))
		done
		;;
	*) cut_skip_reason="unknown cut shape '$1'"; echo "$set_name: $cut_skip_reason"; return 1 ;;
	esac
	if [ -z "$cut_tp" ]; then
		if [ "$_cref_blocked" = 1 ]; then
			cut_skip_reason="every candidate $cut_kind is the paired reference's route (fenced: $ref_tps)"
		else
			cut_skip_reason="no second leg/tunnel to cut"
		fi
		echo "$set_name: $cut_skip_reason — no cut row"
		return 1
	fi
	[ "$cut_pk" != "$exit_pk" ] || {
		cut_skip_reason="the target rides the transport to the EXIT ($cut_tp)"
		echo "$set_name: $cut_skip_reason — refusing to cut it"
		return 1
	}
	cut_short=$(pin_short "$cut_tp" || true)
	if [ -n "${cut_short:-}" ]; then
		# restore by the PIN's pk, not mux info's: `tp add` has to rebuild the
		# exact id the pin names.
		cut_pk=$(pin_pk "$cut_short")
		cut_type=stcpr
	else
		if [ "$CUT_FENCE" = pins ]; then
			cut_skip_reason="$cut_kind $cut_tp is not one of the $(ls "$pins"/via-*.json 2>/dev/null | wc -l | tr -d ' ') pinned hop-1 transports"
			echo "$set_name: $cut_skip_reason — it could not be restored, no cut row"
			return 1
		fi
		# `proxy mux add` can only re-add a leg from a route FILE, so an unpinned
		# leg has nothing to be restored from; only a tunnel's first-hop
		# transport can be rebuilt without a pin (`tp add` gives back the same
		# deterministic id).
		if [ "$cut_kind" = leg ]; then
			cut_skip_reason="leg $cut_tp is not pinned and 'proxy mux add' needs a route file to put it back"
			echo "$set_name: $cut_skip_reason — no cut row"
			return 1
		fi
		[ -n "$cut_pk" ] && [ -n "$cut_type" ] || {
			cut_skip_reason="$cut_kind $cut_tp has no known remote pk/type"
			echo "$set_name: $cut_skip_reason — it could not be restored, no cut row"
			return 1
		}
		echo "$set_name: $cut_tp is not a pinned hop-1 transport; CUT_FENCE=$CUT_FENCE restores it with 'tp add -t $cut_type $cut_pk'"
	fi
	echo "$set_name: cut target = $cut_kind $cut_tp on rg $cut_rg (remote $cut_pk, type $cut_type${cut_short:+, pin via-$cut_short})"
	return 0
}

# do_cut: the one mid-transfer operation. Sets cut_ok.
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
		# a re-dialled transport is not up the instant `tp add` returns, and a row
		# that starts without it strands the next re-establish on the direct
		# transport to the exit — so retry until it is actually back.
		_ra=1
		while [ "$_ra" -le 4 ]; do
			tp_present "$cut_tp" && { echo 1; return; }
			timeout 120 $CLI cli tp add -t "${cut_type:-stcpr}" "$cut_pk" >> "$out/$set_name.cut.log" 2>&1
			sleep 5
			_ra=$((_ra + 1))
		done
		if tp_present "$cut_tp"; then echo 1; else echo 0; fi
		;;
	*) echo 0 ;;
	esac
}

# cut_transfer <label> <dir> <bytes>: ONE hash-verified transfer with ONE cut in
# the middle. The cut fires when the transfer has moved CUT_PCT percent or after
# CUT_AFTER_S seconds, whichever comes first. Progress is the output file for a
# download and the carrier transports' sent counters for an upload (the only
# progress an in-flight buffered POST exposes), so an upload row polls the visor
# once a second through the caller's up_progress.
#
# In:  socks sink tmp cut_after cut_pct, ct_payload / ct_payload_sha for an
#      upload, and do_cut's cut_kind / cut_tp / cut_rg from choose_cut.
# Out: ct_http ct_got ct_secs ct_speed ct_ok ct_cut_at ct_bytes_at_cut ct_ttfb
#      ct_cut_ok — the caller writes the rows, because the two callers write
#      different ones.
cut_transfer() {
	_clabel=$1; _cdir=$2; _csize=$3
	w="$tmp/row"; rm -rf "$w"; mkdir -p "$w"
	want_bytes=$((_csize * cut_pct / 100))
	poll=$CUT_POLL
	if [ "$_cdir" = up ]; then
		poll=1; tp_sent_all > "$tmp/base" 2>/dev/null; : > "$tmp/last"
	fi
	start=$(now)
	if [ "$_cdir" = down ]; then
		curl -s --socks5-hostname "$socks" -m 900 -D "$w/h" -o "$w/b" \
			-w '%{http_code} %{size_download} %{time_total} %{speed_download}' "$sink/?bytes=$_csize" > "$w/w" 2>/dev/null &
	else
		curl -s --socks5-hostname "$socks" -m 900 -o "$w/r" \
			-w '%{http_code} %{size_upload} %{time_total} %{speed_upload}' \
			-X POST --data-binary "@$ct_payload" "$sink/upload" > "$w/w" 2>/dev/null &
	fi
	cpid=$!
	cut_done=0; ct_cut_at=0; ct_bytes_at_cut=0; ct_ttfb=-; cut_ok=0
	while kill -0 "$cpid" 2>/dev/null; do
		e=$(since_s "$start")
		if [ "$_cdir" = down ]; then
			p=0; [ -f "$w/b" ] && p=$(wc -c < "$w/b" | tr -d ' ')
		else
			p=$(up_progress)
		fi
		case $p in '' | *[!0-9]*) p=0 ;; esac
		if [ "$cut_done" -eq 0 ]; then
			if [ "$p" -ge "$want_bytes" ] || ge "$e" "$cut_after"; then
				ct_bytes_at_cut=$p; ct_cut_at=$e
				do_cut
				cut_done=1
				echo "$set_name $_clabel: cut $cut_kind $cut_tp at ${ct_cut_at}s after $ct_bytes_at_cut bytes (ok=$cut_ok)"
			fi
		elif [ "$ct_ttfb" = - ] && [ "$p" -gt "$ct_bytes_at_cut" ]; then
			ct_ttfb=$(awk -v a="$e" -v b="$ct_cut_at" 'BEGIN{printf "%.3f", a - b}')
		fi
		sleep "$poll"
	done
	wait "$cpid" 2>/dev/null
	# shellcheck disable=SC2046 # the four -w fields are split on purpose, as bench.sh does
	set -- $(cat "$w/w" 2>/dev/null)
	ct_http=${1:-000}; ct_got=${2:-0}; ct_secs=${3:-0}; ct_speed=${4:-0}
	if [ "$_cdir" = down ]; then
		_cwant=$(tr -d '\r' < "$w/h" 2>/dev/null | awk 'tolower($1)=="x-sha256:"{print $2}')
		_chave=$(sha256sum "$w/b" 2>/dev/null | cut -d' ' -f1)
	else
		_cwant=$(jq -r .sha256 "$w/r" 2>/dev/null)
		_chave=$ct_payload_sha
	fi
	ct_ok=0; [ -n "$_cwant" ] && [ "$_cwant" = "$_chave" ] && ct_ok=1
	ct_cut_ok=$cut_ok
	# the tail-of-the-object case: the row timed the cut, not the recovery.
	# Downloads only — an upload's progress is the carriers' wire counters, which
	# count more than the payload and cannot be read as a fraction of it.
	ct_pct_at_cut=$((ct_bytes_at_cut * 100 / _csize))
	ct_ttfb_measured=$ct_ttfb
	if [ "$_cdir" = down ] && [ "$ct_pct_at_cut" -ge "$CUT_LATE_PCT" ]; then
		ct_ttfb=late
		echo "$set_name $_clabel: the cut landed at ${ct_pct_at_cut}% of the object (>= ${CUT_LATE_PCT}%) — too late to measure recovery, ttfb recorded as 'late'"
	fi
}
