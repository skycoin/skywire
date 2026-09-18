#!/bin/sh
# direction.sh — which leg carries each direction, per row, from both ends.
#
#   bench/direction.sh bench/<date>/<commit-dir> [set…]
#
# Campaign criterion 5 asks for direction "proven from both ends' per-leg
# counters: forward on the direct or lowest-latency route, reverse fanned over
# multihop legs, flipping on load". This reads the artefacts a bench run already
# writes and turns them into that proof, per ROW (trial × dir × size).
#
# A "leg" here is the first-hop transport a route group rides. A legs set is one
# group over several transports; a tunnels set is several groups of one leg each
# — the same table, direction measured across tunnels instead of across legs.
#
# Fields, and which end they come from:
#
#   local end  <set>.carrier.tsv  per row, per transport: sent_delta (bytes this
#              visor put on the leg = client→exit, FORWARD) and recv_delta
#              (bytes that arrived on the leg = what the exit sent, REVERSE).
#              Its "legs" pseudo-row gives the rg:tp membership at that row.
#   local end  <set>.legs.json    per group and leg: transport_id, tp_type,
#              remote_pk, latency_ms, direct, hops[] and tunnel_role.
#              hops[-1].tp_id is the EXIT's first hop for that leg, which is how
#              the two ends' legs are matched to each other.
#   local end  <set>.tps.tsv      tp → type, remote_pk fallback when a group is
#              not in the legs.json snapshot.
#   exit end   <set>.exit-recovery.tsv  per group and leg, as the exit sees it:
#              tp (its own first hop), standby, retransmits, dup_bytes,
#              ack_delay_ms. The exit's leg record carries NO byte counter, so
#              the exit's sent bytes are read from the local recv_delta of the
#              same leg and the exit record is used to confirm the leg's role
#              and to carry its retransmits/ack delay. Most sets snapshot the
#              exit only at set start and end; standby sets snapshot per row.
#   rows       <set>.tsv          label, dir, size (bench.sh's columns).
#
# Two caveats the data imposes, repeated in every output as "# note" lines:
# carrier.tsv counts a TRANSPORT, not a route group, so a transport shared with
# another group or app counts that traffic too; and latency_ms comes from the
# end-of-set legs.json snapshot, not from the row. To keep the first from
# swamping the verdict, shares and verdicts are computed over the legs of
# ACTIVE groups only when any group of the set is marked active — a standby
# group carries keepalives, not the payload — and over every leg otherwise.
#
# Verdicts, per row:
#   forward_on    the leg that carried >= 80 % of the forward bytes, and whether
#                 it is the direct leg or the lowest-latency leg of that row.
#                 PASS one leg dominates and it is direct or lowest-latency;
#                 FAIL one dominates and it is neither; INFO no leg reaches 80 %
#                 (forward is fanned) or the leg's metadata is missing.
#   reverse_fanout  legs carrying >= 10 % of the reverse bytes, and the largest
#                 single share. PASS on a download of >= 50 MB fanned over >= 2
#                 legs; FAIL on such a download carried by one leg; INFO for
#                 every other cell (an upload's reverse stream is ACKs).
#   flip          whether the dominant forward leg changed from the previous row
#                 of the set. INFO only — the row numbers are listed in a note.
#
# Missing counters are printed as "-" and named in a "# note" line; nothing here
# fails a set because an artefact of an older run shape is absent.
#
# Writes <set>.direction.tsv per set and direction.tsv summarising the dir.
set -u

[ $# -ge 1 ] || { echo "usage: bench/direction.sh <result dir> [set…]" >&2; exit 2; }
dir=$1
shift
[ -d "$dir" ] || { echo "direction.sh: no such directory: $dir" >&2; exit 2; }

tmp=${TMPDIR:-/tmp}/direction.$$
mkdir -p "$tmp" || exit 1
trap 'rm -rf "$tmp"' EXIT INT TERM

sets=$*
if [ -z "$sets" ]; then
	sets=$(for f in "$dir"/*.legs.json; do [ -f "$f" ] || continue; basename "$f" .legs.json; done)
fi

summary=$dir/direction.tsv
printf '# set\trows\tfwd_pass\tfwd_fail\tfwd_info\tdominant_fwd_leg\tdominant_kind\trev_pass\trev_fail\trev_info\tmax_rev_fanout\tflips\tflip_rows\n' > "$summary"

for set_name in $sets; do
	legs_json=$dir/$set_name.legs.json
	carrier=$dir/$set_name.carrier.tsv
	rows=$dir/$set_name.tsv
	if [ ! -f "$legs_json" ] || [ ! -f "$carrier" ] || [ ! -f "$rows" ]; then
		echo "$set_name: skipped (needs .legs.json, .carrier.tsv and .tsv)"
		continue
	fi

	# local end: leg metadata, one line per (rg, leg)
	: > "$tmp/meta.tsv"
	jq -r '
	  .[] as $g
	  | ($g.desc.dst_port // $g.desc.src_port // 0) as $rg
	  | $g.legs[]?
	  | [ (.transport_id // "-")[0:8], ($rg|tostring), ($g.tunnel_role // "-"),
	      (.tp_type // "-"), (.remote_pk // "-"),
	      (if .latency_ms == null then "-" else (.latency_ms|tostring) end),
	      (if .direct then "yes" else "no" end),
	      ([ (.hops // [])[] | ((.tp_id // "-")[0:8]) + ">" + (.to // "-") ] | join(";") | if . == "" then "-" else . end),
	      (((.hops // []) | if length > 0 then .[length-1].tp_id else null end) // "-")[0:8],
	      ((.index // 0)|tostring) ]
	  | @tsv' "$legs_json" > "$tmp/meta.tsv" 2>/dev/null || : > "$tmp/meta.tsv"

	# local end: tp → type, remote_pk fallback
	: > "$tmp/tps.tsv"
	[ -f "$dir/$set_name.tps.tsv" ] &&
		awk -F'\t' '!/^#/ && NF>=3 {print substr($1,1,8) "\t" $2 "\t" $3}' "$dir/$set_name.tps.tsv" > "$tmp/tps.tsv"

	# exit end: per leg, keyed by the exit's own first-hop tp
	: > "$tmp/exit.tsv"
	exit_rec=$dir/$set_name.exit-recovery.tsv
	exit_mode=none
	if [ -f "$exit_rec" ]; then
		awk -F'\t' '!/^#/ && NF>=2 {print $1 "\t" $2}' "$exit_rec" |
		while read -r key blob; do
			printf '%s' "$blob" | jq -r --arg k "$key" '
			  .[]? | (.rg|tostring) as $rg | (.legs // [])[]
			  | [ $k, $rg, (.tp // "-"),
			      (if .standby then "yes" else "no" end),
			      ((.retransmits // 0)|tostring),
			      ((.dup_bytes // 0)|tostring),
			      (if .ack_delay_ms == null then "-" else ((.ack_delay_ms*100|round)/100|tostring) end) ]
			  | @tsv' 2>/dev/null
		done > "$tmp/exit.tsv"
		if awk -F'\t' 'NR>0 && $1 ~ /^[0-9]+$/ {f=1} END{exit !f}' "$tmp/exit.tsv"; then
			exit_mode=per_row
		elif [ -s "$tmp/exit.tsv" ]; then
			exit_mode=start_end
		fi
	fi

	# rows under test, numbered the way carrier.tsv numbers them
	awk -F'\t' '!/^#/ && NF>=3 {i++; print i "\t" $1 "\t" $2 "\t" $3}' "$rows" > "$tmp/rows.tsv"

	out=$dir/$set_name.direction.tsv
	awk -F'\t' -v OFS='\t' \
	    -v set_name="$set_name" -v out="$out" -v summary="$summary" \
	    -v exit_mode="$exit_mode" \
	    -v meta="$tmp/meta.tsv" -v tps="$tmp/tps.tsv" -v xf="$tmp/exit.tsv" -v rf="$tmp/rows.tsv" '
	function share(a, b) { return b > 0 ? a / b : -1 }
	function pct(x) { return x < 0 ? "-" : sprintf("%.1f%%", x * 100) }
	function note(s) { notes[++nn] = s }
	BEGIN {
		while ((getline < meta) > 0) {
			tp = $1
			m_rg[tp]   = (tp in m_rg && index(" " m_rg[tp] " ", " " $2 " ")) ? m_rg[tp] : (tp in m_rg ? m_rg[tp] "," $2 : $2)
			m_role[tp] = $3; m_type[tp] = $4; m_rpk[tp] = $5
			m_lat[tp]  = $6; m_direct[tp] = $7; m_hops[tp] = $8; m_exittp[tp] = $9
			rg_role[$2] = $3
			if ($9 != "-") xtp_of[$2 SUBSEP $9] = tp
		}
		close(meta)
		while ((getline < tps) > 0) { t_type[$1] = $2; t_rpk[$1] = $3 }
		close(tps)
		while ((getline < xf) > 0) {
			k = $1; rg = $2; xtp = $3
			tp = ((rg SUBSEP xtp) in xtp_of) ? xtp_of[rg SUBSEP xtp] : ""
			if (tp == "") { x_unmatched++; continue }
			x_tp[k SUBSEP tp] = xtp; x_sb[k SUBSEP tp] = $4
			x_retx[k SUBSEP tp] = $5; x_ack[k SUBSEP tp] = $7
			x_have[k] = 1
		}
		close(xf)
		while ((getline < rf) > 0) { r_lab[$1] = $2; r_dir[$1] = $3; r_size[$1] = $4; nrows = ($1 > nrows ? $1 : nrows) }
		close(rf)
	}
	# carrier.tsv: the legs membership row, then the per-transport deltas
	/^#/ { next }
	$2 == "legs" { legsline[$1 + 0] = $3; next }
	{
		r = $1 + 0
		tp = substr($2, 1, 8)
		if ($3 ~ /^-?[0-9]+$/) { fwd[r SUBSEP tp] += $3; seen[r SUBSEP tp] = 1 } else missing_fwd++
		if ($4 ~ /^-?[0-9]+$/) { rev[r SUBSEP tp] += $4; seen[r SUBSEP tp] = 1 } else missing_rev++
	}
	END {
		if (missing_fwd) note("sent_delta was not a number on " missing_fwd " carrier line(s); those legs read 0 forward")
		if (missing_rev) note("recv_delta was not a number on " missing_rev " carrier line(s); those legs read 0 reverse")
		if (exit_mode == "none")
			note("no <set>.exit-recovery.tsv: the exit end contributes nothing; exit_tp/exit_standby/exit_retx/exit_ack are \"-\"")
		else if (exit_mode == "start_end")
			note("the exit snapshots only set start and end, so exit_standby/exit_retx/exit_ack are the set-end values repeated on every row, not per-row deltas")
		note("the exit'"'"'s per-leg record has no byte counter (only tp, standby, retransmits, dup_bytes, ack_delay_ms), so exit_bytes is the local recv_delta of the same leg — the bytes the exit sent that arrived there")

		note("carrier.tsv counts a TRANSPORT, not a route group: a transport another group or app also rides counts that traffic here too")
		note("latency_ms is the end-of-set legs.json snapshot, not the latency measured on the row")

		for (r = 1; r <= nrows; r++) {
			if (!(r in r_dir)) continue
			n = split(legsline[r], grp, " ")
			ntp = 0; delete rowtp; delete rowrg
			for (g = 1; g <= n; g++) {
				if (split(grp[g], kv, ":") < 2) continue
				nl = split(kv[2], ls, ",")
				for (l = 1; l <= nl; l++) {
					tp = ls[l]
					if (!(tp in rowrg)) { rowtp[++ntp] = tp; rowrg[tp] = kv[1] }
					else if (!index("," rowrg[tp] ",", "," kv[1] ",")) rowrg[tp] = rowrg[tp] "," kv[1]
				}
			}
			if (ntp == 0) {
				for (k in seen) { split(k, p, SUBSEP); if (p[1] + 0 == r && !(p[2] in rowrg)) { rowtp[++ntp] = p[2]; rowrg[p[2]] = "-" } }
				if (ntp && !warned_legsline++) note("carrier.tsv has no \"legs\" row for some rows; leg membership taken from the transports that reported deltas, rg reads \"-\"")
			}

			# scope: the legs a verdict is computed over. A standby group carries
			# keepalives, so when any group of the row is active only those count.
			# a transport can serve several groups; it is active if ANY of them is
			delete rol_of; nactive = 0
			for (i = 1; i <= ntp; i++) {
				tp = rowtp[i]
				rol = "-"
				ng = split(rowrg[tp], rgs, ",")
				for (j = 1; j <= ng; j++) {
					if (!(rgs[j] in rg_role)) continue
					if (rg_role[rgs[j]] == "active") { rol = "active"; break }
					rol = rg_role[rgs[j]]
				}
				if (rol == "-" && (tp in m_role)) rol = m_role[tp]
				rol_of[tp] = rol
				if (rol == "active") nactive++
			}
			delete inscope
			for (i = 1; i <= ntp; i++) { tp = rowtp[i]; inscope[tp] = (nactive > 0 ? (rol_of[tp] == "active") : 1) }
			if (nactive > 0 && nactive < ntp && !warned_scope++)
				note("shares and verdicts are computed over the legs of ACTIVE groups only; standby legs are listed with their bytes and a \"-\" share")

			ft = 0; rt = 0; minlat = -1
			for (i = 1; i <= ntp; i++) {
				tp = rowtp[i]
				if (!inscope[tp]) continue
				ft += fwd[r SUBSEP tp]; rt += rev[r SUBSEP tp]
				lat = (tp in m_lat) ? m_lat[tp] : "-"
				if (lat != "-" && (minlat < 0 || lat + 0 < minlat)) minlat = lat + 0
			}

			topleg = "-"; topshare = -1; fan = 0; revmax = -1; revmaxleg = "-"
			for (i = 1; i <= ntp; i++) {
				tp = rowtp[i]
				if (!inscope[tp]) continue
				fs = share(fwd[r SUBSEP tp], ft); rs = share(rev[r SUBSEP tp], rt)
				if (fs > topshare) { topshare = fs; topleg = tp }
				if (rs > revmax) { revmax = rs; revmaxleg = tp }
				if (rs >= 0.10) fan++
			}

			key = (exit_mode == "per_row") ? r : "end"
			if (!(key in x_have) && ("end" in x_have)) key = "end"
			for (i = 1; i <= ntp; i++) {
				tp = rowtp[i]
				typ = (tp in m_type) ? m_type[tp] : ((tp in t_type) ? t_type[tp] : "-")
				rpk = (tp in m_rpk) ? m_rpk[tp] : ((tp in t_rpk) ? t_rpk[tp] : "-")
				lat = (tp in m_lat) ? m_lat[tp] : "-"
				hop = (tp in m_hops) ? m_hops[tp] : "-"
				rg  = rowrg[tp]
				rol = rol_of[tp]
				if (!(tp in m_type) && !(tp in t_type) && !warned_meta++)
					note("some transports are in carrier.tsv but not in legs.json or tps.tsv (a group dialled after the snapshot); their carrier/latency/hops/remote_pk read \"-\"")
				xk = key SUBSEP tp
				tbl[++nt] = sprintf("%d\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%d\t%d\t%s\t%s\t%s\t%s\t%s\t%s", \
				  r, r_lab[r], r_dir[r], r_size[r], tp, rg, rol, (inscope[tp] ? "in" : "out"), hop, typ, lat, rpk, \
				  fwd[r SUBSEP tp], rev[r SUBSEP tp], \
				  (inscope[tp] ? pct(share(fwd[r SUBSEP tp], ft)) : "-"), \
				  (inscope[tp] ? pct(share(rev[r SUBSEP tp], rt)) : "-"), \
				  ((xk in x_tp) ? x_tp[xk] : "-"), ((xk in x_sb) ? x_sb[xk] : "-"), \
				  ((xk in x_retx) ? x_retx[xk] : "-"), ((xk in x_ack) ? x_ack[xk] : "-"))
			}

			kind = "-"
			if (topleg != "-" && topleg in m_direct) {
				isd = (m_direct[topleg] == "yes")
				isl = (minlat >= 0 && topleg in m_lat && m_lat[topleg] + 0 == minlat)
				kind = isd && isl ? "direct+lowest-latency" : (isd ? "direct" : (isl ? "lowest-latency" : "other"))
			}
			if (topshare >= 0.80 && kind != "-" && kind != "other")      fv = "PASS"
			else if (topshare >= 0.80 && kind == "other")                fv = "FAIL"
			else                                                         fv = "INFO"
			if (r_dir[r] == "down" && r_size[r] + 0 >= 50000000)         rv = (fan >= 2 ? "PASS" : "FAIL")
			else                                                         rv = "INFO"
			if (fv == "PASS") fp++; else if (fv == "FAIL") ff++; else fi++
			if (rv == "PASS") rp++; else if (rv == "FAIL") rf2++; else ri++
			if (fan > maxfan) maxfan = fan
			dom_count[topleg]++; dom_kind[topleg] = kind

			flip = "-"
			if (prevtop != "" && topleg != "-" && prevtop != "-") {
				if (topleg != prevtop) { flip = "flip:" prevtop "->" topleg; flips++; fliprows = (fliprows == "" ? "" : fliprows ",") r }
				else flip = "same"
			}
			if (topleg != "-") prevtop = topleg

			v_row[r] = sprintf("%d\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%d\t%s\t%s\t%s", \
			  r, r_lab[r], r_dir[r], r_size[r], topleg, kind, pct(topshare), fv, fan, pct(revmax), rv, flip)
			nv++
		}

		printf "# %s — per-row leg shares, forward = client->exit, reverse = exit->client\n", set_name > out
		for (i = 1; i <= nn; i++) printf "# note\t%s\n", notes[i] > out
		printf "# row\tlabel\tdir\tsize\tleg\trg\trole\tscope\thops\tcarrier\tlatency_ms\tremote_pk\tfwd_bytes\trev_bytes\tfwd_share\trev_share\texit_tp\texit_standby\texit_retx\texit_ack_ms\n" > out
		for (i = 1; i <= nt; i++) print tbl[i] > out

		printf "#\n# verdicts\n" > out
		printf "# row\tlabel\tdir\tsize\tforward_on\tforward_kind\tforward_share\tforward\treverse_fanout\treverse_max_share\treverse\tflip\n" > out
		for (r = 1; r <= nrows; r++) if (r in v_row) print v_row[r] > out
		printf "# flip\t%d change(s) of the dominant forward leg%s\n", flips + 0, (fliprows == "" ? "" : " at row(s) " fliprows) > out
		close(out)

		best = "-"; bestn = -1
		for (t in dom_count) if (dom_count[t] > bestn) { bestn = dom_count[t]; best = t }
		printf "%s\t%d\t%d\t%d\t%d\t%s\t%s\t%d\t%d\t%d\t%d\t%d\t%s\n", \
		  set_name, nv + 0, fp + 0, ff + 0, fi + 0, best, (best in dom_kind ? dom_kind[best] : "-"), \
		  rp + 0, rf2 + 0, ri + 0, maxfan + 0, flips + 0, (fliprows == "" ? "-" : fliprows) >> summary

		printf "%-26s rows=%-3d fwd %s %s PASS=%d FAIL=%d INFO=%d | rev fanout<=%d PASS=%d FAIL=%d INFO=%d | flips=%d%s\n", \
		  set_name, nv + 0, best, (best in dom_kind ? dom_kind[best] : "-"), fp + 0, ff + 0, fi + 0, \
		  maxfan + 0, rp + 0, rf2 + 0, ri + 0, flips + 0, (fliprows == "" ? "" : " @r" fliprows)
	}' "$carrier"
done
