#!/bin/sh
# verdict.sh — score a mux results directory against the reference bar.
#
#   bench/verdict.sh <refs dir> <mux dir>
#
# For every mux set and cell (size × direction): the set's median goodput, the
# best reference median for that cell (the bar: criteria 2/3 want >= it), the
# hash-verified count (want 5/5) and wire/goodput (criterion 7 wants <= 1.2).
# PASS needs all three. The reference dir is where run-refs.sh wrote its sets.
#
# Sets marked INVALID by their run script (a <set>.INVALID marker naming the
# reason) never reach the table — they were measured in the wrong shape.
set -u
refs=$1; mux=$2
here=$(dirname "$0")
for m in "$refs"/*.INVALID "$mux"/*.INVALID; do
	[ -f "$m" ] || continue
	echo "INVALID $(basename "$m" .INVALID): $(head -1 "$m")"
done
bar=$("$here/summarize.sh" "$refs" 2>/dev/null | awk 'NR>1 && $1 ~ /^ref-/ {k=$2" "$3; if ($5>b[k]) {b[k]=$5; s[k]=$1}} END{for (k in b) print k, b[k], s[k]}')
printf '%-16s %-5s %-3s %-7s %-7s %-6s %-5s %-6s %s\n' set dir MB median bar ok/n w/g verdict "best ref"
"$here/summarize.sh" "$mux" 2>/dev/null | awk 'NR>1 && $1 ~ /^mux-/' | while read -r set dir mb okn med min max carrier wg; do
	line=$(echo "$bar" | awk -v d="$dir" -v m="$mb" '$1==d && $2==m {print $3, $4}')
	b=${line%% *}; bs=${line#* }
	ok=${okn%%/*}; n=${okn##*/}
	v=PASS
	awk -v med="$med" -v b="$b" 'BEGIN{exit !(med+0 >= b+0)}' || v=FAIL
	[ "$ok" = "$n" ] || v=FAIL
	awk -v wg="$wg" 'BEGIN{exit !(wg+0 <= 1.2)}' || v=FAIL
	printf '%-16s %-5s %-3s %-7s %-7s %-6s %-5s %-6s %s\n' "$set" "$dir" "$mb" "$med" "$b" "$okn" "$wg" "$v" "$bs"
done
