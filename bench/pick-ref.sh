#!/bin/sh
# pick-ref.sh — choose the route the campaign's paired references ride.
#
#   bench/pick-ref.sh <out dir> <pins dir> [exit pk] [sink]
#
# The full eight-set reference suite (run-refs.sh) is a once-a-day run: it
# ORDERS the routes. It cannot be the campaign's bar, because the bar moves
# faster than the suite takes to measure — the 50 MB download reference was
# 8.84, then 6.63, then 5.43 MB/s in three consecutive windows on 2026-09-16.
# What the campaign needs from it is only the SHORTLIST.
#
# So: take the top REF_CANDIDATES (3) routes of the last full suite, probe each
# with REF_TRIALS (2) trials of a PROBE_BYTES (50 MB) download right now, and
# write the winner to <out dir>/paired-ref.txt. run-mux.sh and run-compose.sh
# read that file when PAIRED_REF is unset, so the paired rows of the campaign
# that follows ride the route that is fastest AT CAMPAIGN TIME.
#
# The probe is a download only: upload medians repeat within 5 % across runs
# (direct 50 MB up 9.52 / 9.60 / 9.65 / 10.32 MB/s on four consecutive
# campaigns), downloads swing by 2x — the download is what decides.
#
# Six transfers, about a minute. Rows land in <out dir>/pick-ref.paired-rows.tsv
# (plain bench.sh rows) and the per-candidate medians in
# <out dir>/paired-ref.tsv, so the choice can be re-read later.
#
# EVERY candidate gets a row there — probed, failed (0/N) or merely recorded
# (0/0, carrying the rank the suite or its own reference set gives it). That
# file is what bench/lib-pins.sh ranks the pin order by, and a route missing
# from it is "unranked", which is a worse answer than "known to be bad".
#
# This makes bench/drift-probe.sh unnecessary for a paired campaign: drift asks
# "has the bar moved since it was measured", and a paired campaign measures the
# bar next to every row, so there is no stale bar to drift from.
set -u
CLI=${CLI:-/home/d0mo/go/bin/skywire}
out=${1:-}; pins=${2:-}
exit_pk=${3:-${EXIT_PK:-022716fba0f9a5d46ae7b8a4412296847d63d96c763d49068e851e9be7542080b1}}
sink=${4:-${SINK:-http://127.0.0.1:18080}}
[ -n "$out" ] && [ -n "$pins" ] || {
	echo "usage: bench/pick-ref.sh <out dir> <pins dir> [exit pk] [sink]" >&2
	exit 2
}
here=$(dirname "$0")
PAIRED_HERE=$here
export PAIRED_HERE
# shellcheck source=bench/lib-paired.sh
. "$here/lib-paired.sh"
mkdir -p "$out"
candidates=${REF_CANDIDATES:-3}
trials=${REF_TRIALS:-2}
probe=${PROBE_BYTES:-50000000}
set_name=pick-ref                    # paired_row writes <out>/pick-ref.paired-rows.tsv
f="$out/paired-ref.tsv"

printf '# pick-ref: %s candidates x %s trials of %s bytes down, exit=%s sink=%s\n' \
	"$candidates" "$trials" "$probe" "$exit_pk" "$sink" > "$f"
printf '# candidate\tmedian_MBps\ttrials_ok\tsource_rank_MBps\n' >> "$f"
printf '# pick-ref probe transfers, bench.sh rows\n' > "$out/$set_name.paired-rows.tsv"

shortlist=$(paired_rank "$out" | head -n "$candidates")
if [ -z "$shortlist" ]; then
	echo "pick-ref: no ranked candidates in ${PAIRED_REF_DIR:-$out} (no ref-*.tsv, no drift.tsv) — falling back to 'direct'"
	echo direct > "$out/paired-ref.txt"
	printf 'direct\t-\t0/0\t-\n' >> "$f"
	exit 0
fi
echo "pick-ref: shortlist from the last full suite:"
echo "$shortlist" | sed 's/^/  /'

# The candidates are iterated in THIS shell (a `while read` over a pipe would
# run in a subshell and lose best/best_m).
best=""; best_m=0
for tok in $(echo "$shortlist" | awk '{print $2}'); do
	rank_m=$(echo "$shortlist" | awk -v t="$tok" '$2==t {print $1; exit}')
	if ! paired_start 1 "$tok" "$exit_pk" "$pins" "$sink"; then
		printf '%s\t-\t0/%s\t%s\n' "$tok" "$trials" "$rank_m" >> "$f"
		continue
	fi
	t=1
	while [ "$t" -le "$trials" ]; do
		paired_row 1 "$t" "$probe" down >/dev/null
		t=$((t + 1))
	done
	paired_stop 1
	# only THIS candidate's rows: every candidate's rows carry the same label,
	# so the median is taken over the last <trials> rows, not the whole file.
	m=$(grep -v '^#' "$out/$set_name.paired-rows.tsv" | tail -n "$trials" |
		awk -F'\t' '$8==1 {print $4/1e6}' | sort -n |
		awk '{a[NR]=$1} END{if (!NR) {print "-"; exit} printf "%.2f\n", (NR%2)?a[(NR+1)/2]:(a[NR/2]+a[NR/2+1])/2}')
	ok=$(grep -v '^#' "$out/$set_name.paired-rows.tsv" | tail -n "$trials" | awk -F'\t' '$8==1' | wc -l)
	printf '%s\t%s\t%s/%s\t%s\n' "$tok" "$m" "$ok" "$trials" "$rank_m" >> "$f"
	echo "pick-ref: $tok probed $m MB/s ($ok/$trials hash-verified; the full suite had it at $rank_m)"
	[ "$m" = - ] && continue
	if awk -v a="$m" -v b="$best_m" 'BEGIN{exit !(a + 0 > b + 0)}'; then best=$tok; best_m=$m; fi
done

# EVERY candidate is recorded, not only the ones the shortlist had room to
# probe. A route with no row here reads as UNRANKED downstream
# (bench/lib-pins.sh pin_order) and is then either left out or, with
# PIN_ALLOW_UNRANKED=1, taken on nothing but its name — which is how
# via-0255117b got back into the composition cell of chain AL, whose out dir
# held only a copy of this file and no ref-via-*.tsv. A candidate that was not
# probed carries `-` for its probe median and 0/0 trials, with whatever this
# out dir knows about it in source_rank_MBps: the suite's ranking, or the
# median of its own reference set. That number is what ranks it — or drops it,
# when it says the route cannot carry 50 MB — so the file is self-contained
# wherever it is copied.
probed=$(grep -v '^#' "$f" | cut -f1 | tr '\n' ' ')
ranking=$(paired_rank "$out")
for tok in $(echo "$ranking" | awk '{print $2}') \
	$(ls "$pins"/via-*.json 2>/dev/null | sed 's|.*/via-||; s|\.json$||'); do
	[ -n "$tok" ] || continue
	case " $probed " in *" $tok "*) continue ;; esac
	probed="$probed$tok "
	rank_m=$(echo "$ranking" | awk -v t="$tok" '$2 == t {print $1; exit}')
	if [ -z "$rank_m" ] && refstat=$(pin_ref_stats "$out" "$tok"); then rank_m=${refstat%% *}; fi
	printf '%s\t-\t0/0\t%s\n' "$tok" "${rank_m:--}" >> "$f"
	echo "pick-ref: $tok recorded but not probed (known rank ${rank_m:--} MB/s)"
done

if [ -z "$best" ]; then
	best=$(echo "$shortlist" | awk 'NR==1{print $2}')
	echo "pick-ref: no candidate probed cleanly — keeping the suite's own best, '$best'"
fi
echo "$best" > "$out/paired-ref.txt"
echo "pick-ref: chose '$best' at $best_m MB/s -> $out/paired-ref.txt"
