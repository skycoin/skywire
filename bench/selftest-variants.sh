#!/bin/sh
# selftest-variants.sh — bench/run-variants.sh against the stub CLI, no rig.
#
#   bench/selftest-variants.sh [work dir]
#
# Six assertions, all on artefacts the runner writes, none of which needs a
# visor, a transport or the sink:
#
#   1 INTERLEAVING   the row order is A,B1,A,B2,… per trial and each B row's
#                    pair id names the A row measured immediately before it
#   2 NO LEAK        an A row reads back the values the knobs held before the
#                    run, even though the B before it moved them
#   3 RATIO          with the noise turned off the verdict is exactly the
#                    stub's effect: +30 % PASSes the 1.0 bar, -20 % FAILs it
#   4 RESTORE        after the run every touched knob — local AND exit — is back
#                    at the value it started at
#   5 TRAP           a run killed with TERM mid-row restores them too
#   6 DIAL-TIME      a knob whose catalog doc says it is read when a group is
#                    BUILT is noted as dialtime in the rows that carry it
#
# Exit 0 when every assertion holds, 1 otherwise; each line is PASS or FAIL.
set -u
here=$(dirname "$0")
work=${1:-$(mktemp -d)}
mkdir -p "$work"
fail=0
ok() { echo "PASS $*"; }
no() { echo "FAIL $*"; fail=1; }
# eq <what> <want> <got>
eq() { if [ "$2" = "$3" ]; then ok "$1 ($3)"; else no "$1: want '$2', got '$3'"; fi; }

exit_pk=022716fba0f9a5d46ae7b8a4412296847d63d96c763d49068e851e9be7542080b1

# --- run 1: three variants, noise off ------------------------------------------
out=$work/run1
mkdir -p "$out"
cat > "$out/variants.txt" <<'EOF'
# the control comes first and names no knob: every key is left where it was
baseline
up8	upload.concurrency=8
slow	leg.starve_ratio=12
EOF
echo "--- run 1: baseline / up8 / slow, 2 trials, 10 MB down"
SWEEP_DRY=1 DRY_NOISE=0 SWEEP_SIZES=10 SWEEP_DIRS=down SWEEP_TRIALS=2 \
	"$here/run-variants.sh" "$exit_pk" "$out" "$out/pins" 2 http://127.0.0.1:18080 "" "$out/variants.txt" \
	> "$out/run.log" 2>&1 || { cat "$out/run.log"; no "run 1 exited non-zero"; }
rows="$out/sweep/rows.tsv"
[ -s "$rows" ] || { cat "$out/run.log"; echo "FAIL run 1 produced no rows"; exit 1; }

# 1 interleaving: the variant column, in order
order=$(grep -v '^#' "$rows" | cut -f1 | tr '\n' ' ' | sed 's/ *$//')
eq "1 interleaving" "baseline up8 baseline slow baseline up8 baseline slow" "$order"
pairs=$(grep -v '^#' "$rows" | cut -f4 | tr '\n' ' ' | sed 's/ *$//')
eq "1 pair ids" "1 1 2 2 3 3 4 4" "$pairs"

# 2 no leak: every baseline row reads the pre-run values back
leak=$(grep -v '^#' "$rows" | awk -F'\t' '$1 == "baseline" && $7 !~ /leg.starve_ratio=6/ {n++} END {print n + 0}')
eq "2 baseline rows keep leg.starve_ratio=6" 0 "$leak"
leak2=$(grep -v '^#' "$rows" | awk -F'\t' '$1 == "baseline" && $7 !~ /upload.concurrency=4/ {n++} END {print n + 0}')
eq "2 baseline rows keep upload.concurrency=4" 0 "$leak2"
seen=$(grep -v '^#' "$rows" | awk -F'\t' '$1 == "up8" && $7 ~ /upload.concurrency=8/ {n++} END {print n + 0}')
eq "2 up8 rows read back upload.concurrency=8" 2 "$seen"

# 3 the ratio arithmetic
v="$out/sweep/verdict.tsv"
eq "3 up8 ratio" "1.300" "$(awk -F'\t' '$1 == "up8" {print $3}' "$v")"
eq "3 up8 verdict" "PASS" "$(awk -F'\t' '$1 == "up8" {print $10}' "$v")"
eq "3 slow ratio" "0.800" "$(awk -F'\t' '$1 == "slow" {print $3}' "$v")"
eq "3 slow verdict" "FAIL" "$(awk -F'\t' '$1 == "slow" {print $10}' "$v")"
eq "3 up8 per-pair ratios" "1.300,1.300" "$(awk -F'\t' '$1 == "up8" {print $9}' "$v")"
eq "3 pairs counted" 2 "$(awk -F'\t' '$1 == "slow" {print $6}' "$v")"
eq "3 hashes" "2/2" "$(awk -F'\t' '$1 == "up8" {print $7}' "$v")"

# 4 restore, on both ends
eq "4 local leg.starve_ratio restored" "leg.starve_ratio=6" "$(grep '^leg.starve_ratio=' "$out/dry/local.knobs")"
eq "4 exit leg.starve_ratio restored" "leg.starve_ratio=6" "$(grep '^leg.starve_ratio=' "$out/dry/exit.knobs")"
eq "4 proxy upload.concurrency restored" "upload.concurrency=4" "$(grep '^upload.concurrency=' "$out/dry/proxy.knobs")"
eq "4 restore table written" 2 "$(grep -vc '^#' "$out/sweep/knobs-restore.tsv")"
# the exit really was written DURING the run (the restore above would hide it)
if grep -q "leg.starve_ratio" "$out/sweep/route-exit.json"; then ok "4 exit catalog was read"; else no "4 exit catalog was not read"; fi
# and the rows are the ordinary bench row shape, so summarize.sh reads them
eq "4 up8.tsv rows" 2 "$(grep -vc '^#' "$out/up8.tsv")"
if "$here/summarize.sh" "$out" | grep -q '^up8 '; then ok "4 summarize.sh reads the variant sets"; else no "4 summarize.sh sees no up8 set"; fi

# --- run 2: killed mid-row ------------------------------------------------------
out2=$work/run2
mkdir -p "$out2"
cp "$out/variants.txt" "$out2/variants.txt"
echo "--- run 2: TERM mid-run"
SWEEP_DRY=1 DRY_NOISE=0 DRY_ROW_SLEEP=1 SWEEP_SIZES=10 SWEEP_DIRS=down SWEEP_TRIALS=20 \
	"$here/run-variants.sh" "$exit_pk" "$out2" "$out2/pins" 20 http://127.0.0.1:18080 "" "$out2/variants.txt" \
	> "$out2/run.log" 2>&1 &
p=$!
# wait for the run to be measuring — a fixed sleep raced the catalogs on a
# loaded box and killed it before it had anything to restore
n=0
while [ ! -s "$out2/sweep/rows.tsv" ] || [ "$(grep -vc '^#' "$out2/sweep/rows.tsv")" -lt 2 ]; do
	n=$((n + 1))
	[ "$n" -gt 120 ] && { no "5 the run never reached its second row"; break; }
	sleep 1
done
kill -TERM "$p" 2>/dev/null
wait "$p" 2>/dev/null
if grep -q 'leg.starve_ratio=12' "$out2/dry/local.knobs"; then no "5 TERM left the local visor tuned"; else ok "5 TERM restored the local visor"; fi
if grep -q 'upload.concurrency=8' "$out2/dry/proxy.knobs"; then no "5 TERM left the app tuned"; else ok "5 TERM restored the app"; fi
if grep -q 'knobs restored' "$out2/run.log"; then ok "5 the trap said so"; else no "5 no restore line in the log"; fi

# --- run 3: a dial-time knob ----------------------------------------------------
out3=$work/run3
mkdir -p "$out3"
cat > "$out3/variants.txt" <<'EOF'
baseline
nosack	mux.sack=false
EOF
echo "--- run 3: a knob that only bites NEW route groups"
SWEEP_DRY=1 DRY_NOISE=0 SWEEP_SIZES=10 SWEEP_DIRS=down SWEEP_TRIALS=1 \
	"$here/run-variants.sh" "$exit_pk" "$out3" "$out3/pins" 1 http://127.0.0.1:18080 "" "$out3/variants.txt" \
	> "$out3/run.log" 2>&1 || no "run 3 exited non-zero"
eq "6 every row notes the dial-time knob" 2 "$(grep -vc '^#' "$out3/sweep/rows.tsv"; )"
if grep -v '^#' "$out3/sweep/rows.tsv" | awk -F'\t' '$8 !~ /dialtime:mux.sack/ {bad++} END {exit bad > 0}'; then
	ok "6 dialtime:mux.sack noted on every row"
else
	no "6 a row is missing the dialtime note"
fi

# --- run 4: a knob in neither catalog stops the run -----------------------------
out4=$work/run4
mkdir -p "$out4"
printf 'baseline\nbogus\tno.such_knob=1\n' > "$out4/variants.txt"
if SWEEP_DRY=1 "$here/run-variants.sh" "$exit_pk" "$out4" "$out4/pins" 1 http://127.0.0.1:18080 "" "$out4/variants.txt" > "$out4/run.log" 2>&1; then
	no "7 an unknown knob was accepted"
else
	if grep -q 'not in either catalog' "$out4/run.log"; then
		ok "7 an unknown knob stops the run before any row"
	else
		no "7 wrong refusal: $(tail -1 "$out4/run.log")"
	fi
fi

echo "---"
if [ "$fail" = 0 ]; then echo "selftest-variants: all assertions hold ($work)"; else echo "selftest-variants: FAILURES above ($work)"; fi
exit "$fail"
