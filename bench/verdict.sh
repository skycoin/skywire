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
#                         at 50 MB and 100 MB: ratio >= the better of the two
#                         ("… and >= the better of the two alone"). When those
#                         two sets are not in the directory the cell is marked
#                         NOCOMP and scored by the plain ratio rule.
#   every cell            all rows hash-verified, and wire/goodput <= 1.2
#                         (criterion 7's amplification gate).
#
# The two-upload cell is scored separately, from <set>.up2.tsv: the sum of two
# concurrent uploads against the sum of the two best single-route references.
#
# Sets marked INVALID by their run script (a <set>.INVALID marker naming the
# reason) never reach the table — they were measured in the wrong shape.
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

printf '%-18s %-5s %-4s %-7s %-8s %-6s %-5s %-7s %-7s %s\n' set dir MB median bar/ratio ok/n w/g mode verdict note
"$here/summarize.sh" "$mux" 2>/dev/null | awk 'NR>1 && $1 ~ /^mux-/' | while read -r set dir mb okn med min max carrier wg; do
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
				# ">= the better of the two alone" at 50 MB and 100 MB
				note="vs better of t2 $t2 l2 $l2"
				awk -v r="$ratio" -v a="$t2" -v b="$l2" \
					'BEGIN{best=(a+0>b+0?a+0:b+0); exit !(r+0 >= best)}' || v=FAIL
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
for u in "$mux"/*.up2.tsv; do
	[ -f "$u" ] || continue
	s=$(basename "$u" .up2.tsv)
	[ -f "$mux/$s.INVALID" ] && continue
	grep -v '^#' "$u" | awk -F'\t' -v s="$s" -v t="$tol" '
		$8 != "-" { r[++k] = $8 + 0; sum += $4; rs += $7; ok += ($9 == 1 && $10 == 1) }
		END {
			if (!k) { printf "%-18s two concurrent uploads: no scored trials\n", s; exit }
			# median of the k ratios, by insertion sort (mawk has no asort)
			for (i = 2; i <= k; i++) { v = r[i]; j = i - 1; while (j > 0 && r[j] > v) { r[j+1] = r[j]; j-- } r[j+1] = v }
			med = (k % 2) ? r[(k+1)/2] : (r[k/2] + r[k/2+1]) / 2
			printf "%-18s two concurrent uploads: sum %.2f vs ref sum %.2f, median ratio x%.3f over %d trial(s), %d hash-clean -> %s\n", \
				s, sum/k, rs/k, med, k, ok, (med >= t + 0 && ok == k ? "PASS" : "FAIL")
		}'
done

# --- the cut row --------------------------------------------------------------
for cf in "$mux"/*.cut.tsv; do
	[ -f "$cf" ] || continue
	s=$(basename "$cf" .cut.tsv)
	head -1 "$cf" | grep -q 'first_hop_pk' || continue # run-degrade.sh's own cut file has another shape
	grep -v '^#' "$cf" | awk -F'\t' -v s="$s" \
		'{printf "%-18s cut row %s: %s (first hop %s) ttfb_after_cut %ss, rg %s -> %s, cut_ok=%s restored=%s\n", s, $1, $2, $3, $5, $6, $7, $8, $9}'
done

# --- the exit resource gate ---------------------------------------------------
if [ -f "$mux/exit-resources.tsv" ]; then
	for s in $(grep -v '^#' "$mux/exit-resources.tsv" | awk -F'\t' '$2 ~ /-pre$/ {sub(/-pre$/, "", $2); print $2}'); do
		"$here/exit-resources-check.sh" "$mux" "$s" || true
	done
fi
