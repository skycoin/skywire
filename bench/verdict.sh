#!/bin/sh
# verdict.sh — score a mux results directory.
#
#   bench/verdict.sh <refs dir> <mux dir>
#
# TWO MODES, and the table says which one every row used:
#
# paired  — the set has a <set>.paired.tsv, so every one of its rows was run
#           immediately after a reference row of the SAME cell on one single
#           route (bench/lib-paired.sh). The score is the MEDIAN PAIRED RATIO
#           mux/reference over that cell's rows, which is what the 2026-09-17
#           goal text asks for: "every verdict is the paired ratio
#           mux/best-reference". A ratio compares two transfers seconds apart,
#           so it survives a bar that swings 2x inside an hour.
# bar     — no paired file (an old result dir, or PAIRED=0): the cell's median
#           is compared against the best reference median for that cell in
#           <refs dir>, exactly as this script always did.
#
# PASS in paired mode needs all of:
#   download cells        ratio >= TOL (0.95 — "within a 5 % noise band")
#   single upload cells   ratio >= TOL
#   composition           mux-compose-* at 10 MB: ratio >= TOL x the ratio of
#                         BOTH mux-tunnels-2 and mux-legs-2 in the same cell of
#                         the same run ("not worse than each alone at 10 MB");
#                         at 50 MB and 100 MB: the v4 rule below. When those two
#                         sets are not in the directory the cell is marked
#                         NOCOMP and scored by the plain ratio rule.
#   every cell            all rows hash-verified, and wire/goodput <= 1.2
#                         (criterion 7's amplification gate).
#
# The two-upload cell is scored separately, from <set>.up2.tsv: the sum of two
# concurrent uploads against the bar below.
#
# THE ENDPOINT CEILING (v4, 2026-09-18). When the directory holds a
# `ceiling.tsv` — bench/run-ceiling.sh, N concurrent clients measured in the
# SAME campaign: the uplink over N DIRECT clients, the downlink over the N best
# DISTINCT routes of the paired ranking, because the direct stcpr path is itself
# the download bottleneck (2026-09-17: 5.04 MB/s over three direct clients while
# one download via-0371ab4b did 8-9.5 in the same hour) — two verdicts are then
# scored against the endpoint instead of against skywire alone:
#
#   two uploads   sum >= min(ref1 + ref2, 0.95 x the uplink ceiling), and the
#                 line says which of the two bounds bound it.
#   composition   at 50 and 100 MB DOWN: if the better of mux-tunnels-2 and
#                 mux-legs-2 in that cell does NOT saturate the endpoint (its
#                 median rate < 0.9 x the downlink ceiling) the bar is the
#                 better of the two alone within the 5 % band; otherwise the
#                 component has the endpoint and the bar is 0.95 x the ceiling.
#
# Without a ceiling.tsv both keep exactly today's rule and say "no ceiling row"
# — a ceiling quoted from an older campaign is not a bar, the link moves by 2x
# inside an hour.
#
# Sets marked INVALID by their run script (a <set>.INVALID marker naming the
# reason) never reach the table — they were measured in the wrong shape.
# A set that writes a <set>.assert.tsv (the spread set of run-mux.sh, the
# standby set of run-standby.sh) has that table printed verbatim at the end.
set -u
refs=$1; mux=$2
here=$(dirname "$0")
tol=${PAIRED_TOL:-0.95}
wg_max=${WG_MAX:-1.2}
tmp=$(mktemp -d); trap 'rm -rf "$tmp"' EXIT INT TERM

for m in "$refs"/*.INVALID "$mux"/*.INVALID; do
	[ -f "$m" ] || continue
	echo "INVALID $(basename "$m" .INVALID): $(head -1 "$m")"
done

# --- the endpoint ceiling, when the campaign measured one ---------------------
ceil_file=""
for c in "$mux/ceiling.tsv" "$refs/ceiling.tsv"; do
	[ -f "$c" ] && { ceil_file=$c; break; }
done
# ceil_of <uplink|downlink>: the median of the CONCURRENT sums — the rows with
# the most clients — or "" when there is no such row.
ceil_of() {
	[ -n "$ceil_file" ] || return 0
	awk -F'\t' -v k="$1" '
		/^#/ { next }
		$1 == k { rows[++m] = $2 "\t" $5; if ($2 + 0 > mc) mc = $2 + 0 }
		END {
			for (i = 1; i <= m; i++) { split(rows[i], f, "\t"); if (f[1] + 0 == mc) v[++n] = f[2] + 0 }
			if (!n) exit
			for (i = 2; i <= n; i++) { x = v[i]; j = i - 1; while (j > 0 && v[j] > x) { v[j+1] = v[j]; j-- } v[j+1] = x }
			printf "%.2f", (n % 2) ? v[(n+1)/2] : (v[n/2] + v[n/2+1]) / 2
		}' "$ceil_file"
}
ceil_up=$(ceil_of uplink); ceil_down=$(ceil_of downlink)
if [ -n "$ceil_file" ]; then
	# the header line run-ceiling.sh writes names the downlink routes after its dash
	ceil_routes=$(awk -F' — ' '/^# downlink rows:/ {print $NF; exit}' "$ceil_file")
	echo "endpoint ceiling ($ceil_file): uplink ${ceil_up:--} MB/s over direct clients, downlink ${ceil_down:--} MB/s over ${ceil_routes:-the routes it recorded} (median of the concurrent sums)"
else
	echo "no ceiling row: no ceiling.tsv in $mux or $refs — the two-upload and composition cells keep the pre-v4 rule"
fi

# --- paired ratios: one median per set and cell -------------------------------
: > "$tmp/pratio"
for p in "$mux"/*.paired.tsv; do
	[ -f "$p" ] || continue
	s=$(basename "$p" .paired.tsv)
	[ -f "$mux/$s.INVALID" ] && continue
	for cell in $(grep -v '^#' "$p" | cut -f2 | sort -u); do
		med=$(grep -v '^#' "$p" | awk -F'\t' -v c="$cell" '$2==c && $5!="-" {print $5}' | sort -n |
			awk '{a[NR]=$1} END{if (!NR) exit; printf "%.3f", (NR%2)?a[(NR+1)/2]:(a[NR/2]+a[NR/2+1])/2}')
		[ -n "$med" ] || continue
		n=$(grep -v '^#' "$p" | awk -F'\t' -v c="$cell" '$2==c && $5!="-"' | wc -l)
		ref=$(head -1 "$p" | sed 's/^# paired reference: //; s/ —.*//')
		printf '%s %s %s %s %s\n' "$s" "$cell" "$med" "$n" "$ref" >> "$tmp/pratio"
	done
done
if [ -s "$tmp/pratio" ]; then
	echo "paired references in use: $(awk '{print $5}' "$tmp/pratio" | sort -u | tr '\n' ' ')"
fi

# --- the old bar, for any set that has no paired file -------------------------
bar=$("$here/summarize.sh" "$refs" 2>/dev/null | awk 'NR>1 && $1 ~ /^ref-/ {k=$2" "$3; if ($5>b[k]) {b[k]=$5; s[k]=$1}} END{for (k in b) print k, b[k], s[k]}')

# one summarize pass over the mux dir: the table below and the per-cell medians
# the composition rule needs for its components read the same rows.
"$here/summarize.sh" "$mux" 2>/dev/null > "$tmp/sum"
awk 'NR>1 && $1 ~ /^mux-/ {print $1, $3 $2, $5}' "$tmp/sum" > "$tmp/med"

printf '%-18s %-5s %-4s %-7s %-8s %-6s %-5s %-7s %-7s %s\n' set dir MB median bar/ratio ok/n w/g mode verdict note
awk 'NR>1 && $1 ~ /^mux-/' "$tmp/sum" | while read -r set dir mb okn med min max carrier wg; do
	: "$min $max $carrier" # summarize's columns, not scored here
	cell="$mb$dir"
	ok=${okn%%/*}; n=${okn##*/}
	note=-
	ratio=$(awk -v s="$set" -v c="$cell" '$1==s && $2==c {print $3}' "$tmp/pratio")
	if [ -n "$ratio" ]; then
		mode=paired
		shown="x$ratio"
		v=PASS
		case $set in
		mux-compose-*)
			t2=$(awk -v c="$cell" '$1=="mux-tunnels-2" && $2==c {print $3}' "$tmp/pratio")
			l2=$(awk -v c="$cell" '$1=="mux-legs-2" && $2==c {print $3}' "$tmp/pratio")
			if [ -z "$t2" ] || [ -z "$l2" ]; then
				note=NOCOMP
				awk -v r="$ratio" -v t="$tol" 'BEGIN{exit !(r+0 >= t+0)}' || v=FAIL
			elif [ "$mb" -le 10 ]; then
				# "not worse than each alone at 10 MB"
				note="vs t2 $t2 l2 $l2"
				awk -v r="$ratio" -v a="$t2" -v b="$l2" -v t="$tol" \
					'BEGIN{exit !(r+0 >= t*a && r+0 >= t*b)}' || v=FAIL
			else
				# v4: ">= the better of the two alone WHEN that better one does
				# not saturate the endpoint, otherwise >= 0.95 x the ceiling"
				brate=$(awk -v c="$cell" '($1=="mux-tunnels-2" || $1=="mux-legs-2") && $2==c {if ($3+0>b) b=$3+0} END{printf "%.2f", b+0}' "$tmp/med")
				cl=""; [ "$dir" = down ] && cl=$ceil_down
				if [ -z "$cl" ]; then
					note="vs better of t2 $t2 l2 $l2, no ceiling row"
					awk -v r="$ratio" -v a="$t2" -v b="$l2" \
						'BEGIN{best=(a+0>b+0?a+0:b+0); exit !(r+0 >= best)}' || v=FAIL
				elif awk -v r="$brate" -v c="$cl" 'BEGIN{exit !(r+0 < 0.9*c)}'; then
					note="vs better of t2 $t2 l2 $l2 (5 % band); best $brate < 0.9 x ceiling $cl"
					awk -v r="$ratio" -v a="$t2" -v b="$l2" -v t="$tol" \
						'BEGIN{best=(a+0>b+0?a+0:b+0); exit !(r+0 >= t*best)}' || v=FAIL
				else
					note="best $brate saturates ceiling $cl — bar 0.95 x ceiling = $(awk -v c="$cl" 'BEGIN{printf "%.2f", 0.95*c}')"
					awk -v m="$med" -v c="$cl" 'BEGIN{exit !(m+0 >= 0.95*c)}' || v=FAIL
				fi
			fi
			;;
		*)
			awk -v r="$ratio" -v t="$tol" 'BEGIN{exit !(r+0 >= t+0)}' || v=FAIL
			;;
		esac
	else
		mode=bar
		line=$(echo "$bar" | awk -v d="$dir" -v m="$mb" '$1==d && $2==m {print $3, $4}')
		b=${line%% *}; bs=${line#* }
		if [ -z "$line" ]; then
			shown="-"; v=NOBAR; note="no $mb MB $dir reference in $refs"
		else
			shown=$b; note=$bs; v=PASS
			awk -v med="$med" -v b="$b" 'BEGIN{exit !(med+0 >= b+0)}' || v=FAIL
		fi
	fi
	[ "$ok" = "$n" ] || { v=FAIL; note="$note hash $okn"; }
	awk -v wg="$wg" -v x="$wg_max" 'BEGIN{exit !(wg+0 <= x+0)}' || { v=FAIL; note="$note w/g $wg"; }
	printf '%-18s %-5s %-4s %-7s %-8s %-6s %-5s %-7s %-7s %s\n' \
		"$set" "$dir" "$mb" "$med" "$shown" "$okn" "$wg" "$mode" "$v" "$note"
done

# --- the two-upload cell ------------------------------------------------------
# v4: the bar is min(the two references' sum, 0.95 x the uplink ceiling), scored
# on the MEDIAN per-trial sum. With no ceiling.tsv it is the pre-v4 rule: the
# median per-trial ratio against TOL.
for u in "$mux"/*.up2.tsv; do
	[ -f "$u" ] || continue
	s=$(basename "$u" .up2.tsv)
	[ -f "$mux/$s.INVALID" ] && continue
	grep -v '^#' "$u" | awk -F'\t' -v s="$s" -v t="$tol" -v cu="$ceil_up" '
		function msort(a, n,   i, j, x) { for (i = 2; i <= n; i++) { x = a[i]; j = i - 1; while (j > 0 && a[j] > x) { a[j+1] = a[j]; j-- } a[j+1] = x } }
		function med(a, n) { msort(a, n); return (n % 2) ? a[(n+1)/2] : (a[n/2] + a[n/2+1]) / 2 }
		$8 != "-" { k++; r[k] = $8 + 0; sm[k] = $4 + 0; rf[k] = $7 + 0; ok += ($9 == 1 && $10 == 1) }
		END {
			if (!k) { printf "%-18s two concurrent uploads: no scored trials\n", s; exit }
			mr = med(r, k); ms = med(sm, k); mf = med(rf, k)
			if (cu == "") {
				printf "%-18s two concurrent uploads: sum %.2f vs ref sum %.2f, median ratio x%.3f over %d trial(s), %d hash-clean, no ceiling row -> %s\n", \
					s, ms, mf, mr, k, ok, (mr >= t + 0 && ok == k ? "PASS" : "FAIL")
				exit
			}
			cb = 0.95 * cu
			bar = (mf < cb ? mf : cb)
			printf "%-18s two concurrent uploads: sum %.2f vs bar %.2f = min(ref sum %.2f, 0.95 x uplink ceiling %.2f = %.2f) — bound by %s; median ratio x%.3f over %d trial(s), %d hash-clean -> %s\n", \
				s, ms, bar, mf, cu, cb, (mf < cb ? "the reference sum" : "the ceiling"), mr, k, ok, \
				(ms >= bar && ok == k ? "PASS" : "FAIL")
		}'
done

# --- the cut row --------------------------------------------------------------
for cf in "$mux"/*.cut.tsv; do
	[ -f "$cf" ] || continue
	s=$(basename "$cf" .cut.tsv)
	head -1 "$cf" | grep -q 'first_hop_pk' || continue # run-degrade.sh's own cut file has another shape
	# a set whose cut was fenced off (the only target left was the paired
	# reference's route) records the reason instead of a row
	if [ -z "$(grep -v '^#' "$cf")" ]; then
		_vr=$(sed -n 's/^# cut=skipped://p' "$cf" | head -1)
		printf '%-18s cut row: skipped — %s\n' "$s" "${_vr:-no row recorded}"
		continue
	fi
	# ttfb_after_cut = `late`: the cut landed on the tail of the object, with no
	# traffic left for the recovery to carry. That row is INVALID — it timed the
	# cut, not the router — and is never read as a failure to recover
	# (bench/lib-cut.sh CUT_LATE_PCT).
	grep -v '^#' "$cf" | awk -F'\t' -v s="$s" \
		'{ t = ($5 == "late" ? "INVALID (cut landed too late to measure)" : $5 "s");
		   printf "%-18s cut row %s: %s (first hop %s) ttfb_after_cut %s, rg %s -> %s, cut_ok=%s restored=%s\n", s, $1, $2, $3, t, $6, $7, $8, $9}'
done

# --- per-set assert tables (the spread set, the standby set) ------------------
for af in "$mux"/*.assert.tsv; do
	[ -f "$af" ] || continue
	s=$(basename "$af" .assert.tsv)
	[ -f "$mux/$s.INVALID" ] && continue
	echo "--- $s asserts"
	cat "$af"
done

# --- the exit resource gate ---------------------------------------------------
if [ -f "$mux/exit-resources.tsv" ]; then
	for s in $(grep -v '^#' "$mux/exit-resources.tsv" | awk -F'\t' '$2 ~ /-pre$/ {sub(/-pre$/, "", $2); print $2}'); do
		"$here/exit-resources-check.sh" "$mux" "$s" || true
	done
fi
