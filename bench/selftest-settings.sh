#!/bin/sh
# shellcheck disable=SC2034,SC2154,SC1091 # $CLI, $out, $exit_pk, $SETTINGS and $settings_note cross the
# `. lib-settings.sh` source boundary: the library reads the first four and sets the last.
# selftest-settings.sh — bench/lib-settings.sh against the stub CLI, no rig.
#
#   bench/selftest-settings.sh [work dir]
#
# The library gained one rule on 2026-09-18: a SETTINGS key that is in the
# ROUTER catalog goes to `route settings` on BOTH ends instead of to the app,
# where it would have been refused and taken the whole SETTINGS line down with
# it. These assertions pin that rule and its restore:
#
#   1 SPLIT      a router key reaches the local visor and the exit; a proxy key
#                reaches the app; neither is sent to the other command
#   2 SNAPSHOT   <set>.route-knobs.tsv holds what the router keys were before
#   3 RESTORE    settings_restore puts the router keys back on both ends, and
#                leaves the app knobs alone (they die with the app)
#   4 UNCHANGED  a SETTINGS line of proxy keys only behaves exactly as before
#
# The stub is bench/dry-stub-cli.sh, the same fake CLI the run-variants dry run
# uses: the knob state is three key=value files, so an apply is verifiable by
# reading them.
set -u
here=$(dirname "$0")
work=${1:-$(mktemp -d)}
mkdir -p "$work"
fail=0
ok() { echo "PASS $*"; }
no() { echo "FAIL $*"; fail=1; }
eq() { if [ "$2" = "$3" ]; then ok "$1 ($3)"; else no "$1: want '$2', got '$3'"; fi; }

exit_pk=022716fba0f9a5d46ae7b8a4412296847d63d96c763d49068e851e9be7542080b1
out=$work/out
mkdir -p "$out"
SWEEP_DRY_STATE=$work/state
export SWEEP_DRY_STATE
CLI=$here/dry-stub-cli.sh
# shellcheck source=bench/lib-settings.sh
. "$here/lib-settings.sh"

# --- 1/2 the split, and the snapshot -------------------------------------------
SETTINGS="leg.starve_ratio=12 upload.concurrency=8"
settings_apply mixed skysocks-client > "$work/apply.log" 2>&1
eq "1 router key went to the local visor" "leg.starve_ratio=12" "$(grep '^leg.starve_ratio=' "$SWEEP_DRY_STATE/local.knobs")"
eq "1 router key went to the EXIT" "leg.starve_ratio=12" "$(grep '^leg.starve_ratio=' "$SWEEP_DRY_STATE/exit.knobs")"
eq "1 proxy key went to the app" "upload.concurrency=8" "$(grep '^upload.concurrency=' "$SWEEP_DRY_STATE/proxy.knobs")"
if grep -q 'REFUSED' "$work/apply.log"; then no "1 something was refused: $(grep REFUSED "$work/apply.log" | head -1)"; else ok "1 nothing was refused"; fi
eq "2 the snapshot holds the previous value" "leg.starve_ratio	6" "$(grep -v '^#' "$out/mixed.route-knobs.tsv")"
case $settings_note in
*router=leg.starve_ratio=12*) ok "2 the set header names the router half" ;;
*) no "2 the header does not name the router half: $settings_note" ;;
esac
case $settings_note in
*router_exit=ok*) ok "2 the header says the exit took it" ;;
*) no "2 the header does not say the exit took it: $settings_note" ;;
esac

# --- 3 restore -----------------------------------------------------------------
settings_restore mixed > "$work/restore.log" 2>&1
eq "3 the local visor is back" "leg.starve_ratio=6" "$(grep '^leg.starve_ratio=' "$SWEEP_DRY_STATE/local.knobs")"
eq "3 the exit is back" "leg.starve_ratio=6" "$(grep '^leg.starve_ratio=' "$SWEEP_DRY_STATE/exit.knobs")"
eq "3 the app knob is left where the set put it" "upload.concurrency=8" "$(grep '^upload.concurrency=' "$SWEEP_DRY_STATE/proxy.knobs")"

# --- 4 a proxy-only line is unchanged -------------------------------------------
SETTINGS="chunk.per_tunnel=4"
settings_apply proxyonly skysocks-client > "$work/apply2.log" 2>&1
eq "4 the proxy key landed" "chunk.per_tunnel=4" "$(grep '^chunk.per_tunnel=' "$SWEEP_DRY_STATE/proxy.knobs")"
if [ -f "$out/proxyonly.route-knobs.tsv" ]; then no "4 a proxy-only line wrote a router snapshot"; else ok "4 no router snapshot for a proxy-only line"; fi
case $settings_note in
*settings=*) ok "4 the header is the ordinary settings note" ;;
*) no "4 unexpected header: $settings_note" ;;
esac

echo "---"
if [ "$fail" = 0 ]; then echo "selftest-settings: all assertions hold ($work)"; else echo "selftest-settings: FAILURES above ($work)"; fi
exit "$fail"
