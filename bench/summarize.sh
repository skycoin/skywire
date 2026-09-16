#!/bin/sh
# summarize.sh — one results table from a bench/<date>/<commit> directory.
#
#   bench/summarize.sh bench/<date>/<commit>
#
# Per set, size and direction: trials, hash-verified trials, median / min / max
# goodput in MB/s, and — when <set>.carrier.tsv exists — how many rows moved at
# least the transfer size over the transport they were supposed to ride.
set -u
dir=$1
printf '%-24s %-5s %-6s %-6s %-9s %-9s %-9s %s\n' set dir MB ok/n median min max carrier
for f in "$dir"/*.tsv; do
	case $f in *.carrier.tsv) continue ;; esac
	set_name=$(basename "$f" .tsv)
	c="$dir/$set_name.carrier.tsv"
	for n in 10000000 50000000; do
		for d in down up; do
			rows=$(grep -v '^#' "$f" | awk -F'\t' -v n="$n" -v d="$d" '$2==d && $3==n')
			[ -z "$rows" ] && continue
			cnt=$(echo "$rows" | wc -l)
			ok=$(echo "$rows" | awk -F'\t' '$8==1' | wc -l)
			stats=$(echo "$rows" | awk -F'\t' '{print $4/1e6}' | sort -n | awk '{a[NR]=$1} END{m=(NR%2)?a[(NR+1)/2]:(a[NR/2]+a[NR/2+1])/2; printf "%.2f %.2f %.2f", m, a[1], a[NR]}')
			carrier=-
			if [ -f "$c" ]; then
				# rows of this size+dir, in file order, matched to carrier rows by index
				idx=$(grep -v '^#' "$f" | awk -F'\t' -v n="$n" -v d="$d" '{i++} $2==d && $3==n {print i}')
				moved=0; tot=0
				for i in $idx; do
					tot=$((tot + 1))
					col=4; [ "$d" = up ] && col=3
					delta=$(awk -F'\t' -v i="$i" -v col="$col" '$1==i {print $col; exit}' "$c")
					case $delta in ""|*[!0-9]*) ;; *) [ "$delta" -ge "$n" ] && moved=$((moved + 1)) ;; esac
				done
				carrier="$moved/$tot"
			fi
			printf '%-24s %-5s %-6s %-6s %-9s %-9s %-9s %s\n' "$set_name" "$d" "$((n / 1000000))" "$ok/$cnt" $stats "$carrier"
		done
	done
done
