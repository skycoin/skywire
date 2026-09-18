#!/bin/sh
# summarize.sh — one results table from a bench/<date>/<commit> directory.
#
#   bench/summarize.sh bench/<date>/<commit>
#
# Per set, size and direction: trials, hash-verified trials, median / min / max
# goodput in MB/s, and — when <set>.carrier.tsv exists — how many rows moved at
# least the transfer size over the transport they were supposed to ride.
#
# A set whose run script could not put the rig in the target shape leaves a
# <set>.INVALID marker holding the reason. Such a set is named once and then
# skipped entirely: whatever rows it managed to write measure an unknown shape.
#
# The cell sizes are read from the rows themselves rather than assumed, so a run
# that added the 100 MB download cell (SIZES / CELL100) summarizes with no
# change here, and an old result dir summarizes exactly as it always did.
set -u
dir=$1
for m in "$dir"/*.INVALID; do
	[ -f "$m" ] || continue
	echo "INVALID $(basename "$m" .INVALID): $(head -1 "$m")"
done
printf '%-24s %-5s %-6s %-6s %-9s %-9s %-9s %-8s %s\n' set dir MB ok/n median min max carrier wire/good
for f in "$dir"/*.tsv; do
	# companion files of a set, not sets: they hold other columns entirely
	case $f in
	*.carrier.tsv | *.recovery.tsv | *.exit-recovery.tsv | *.paired.tsv | *.paired-rows.tsv | *.cut.tsv | *.up2.tsv | *.tps.tsv) continue ;;
	*.assert.tsv | *.direction.tsv | *.settings.tsv) continue ;;
	*/exit-resources.tsv | */paired-ref.tsv | */ceiling.tsv | */direction.tsv) continue ;;
	esac
	set_name=$(basename "$f" .tsv)
	[ -f "$dir/$set_name.INVALID" ] && continue
	c="$dir/$set_name.carrier.tsv"
	for n in $(grep -v '^#' "$f" | awk -F'\t' '$3 ~ /^[0-9]+$/ {print $3}' | sort -n -u); do
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
				# a row "moved" when the set's carriers TOGETHER moved the transfer (a mux set names several)
				for i in $idx; do
					tot=$((tot + 1))
					col=4; [ "$d" = up ] && col=3
					delta=$(awk -F'\t' -v i="$i" -v col="$col" '$1==i && $2!="legs" && $col ~ /^[0-9]+$/ {s+=$col} END{print s+0}' "$c")
					[ "$delta" -ge "$n" ] && moved=$((moved + 1))
				done
				carrier="$moved/$tot"
			fi
				# wire/goodput: bytes moved over the named carrier(s) in the transfer direction ÷ transfer size, median over rows
				wire=-
				if [ -f "$c" ]; then
					col=4; [ "$d" = up ] && col=3
					wire=$(for i in $idx; do awk -F'\t' -v i="$i" -v col="$col" -v n="$n" '$1==i && $2!="legs" && $col ~ /^[0-9]+$/ {s+=$col} END{if (s>0) printf "%.3f\n", s/n}' "$c"; done | sort -n | awk '{a[NR]=$1} END{if (NR) printf "%.2f", (NR%2)?a[(NR+1)/2]:(a[NR/2]+a[NR/2+1])/2; else print "-"}')
				fi
			printf '%-24s %-5s %-6s %-6s %-9s %-9s %-9s %-8s %s\n' "$set_name" "$d" "$((n / 1000000))" "$ok/$cnt" $stats "$carrier" "$wire"
		done
	done
done
