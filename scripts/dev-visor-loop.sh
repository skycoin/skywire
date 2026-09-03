#!/usr/bin/env bash
# dev-visor-loop.sh — rebuild + run the visor in a loop, preserving logs on crash.
#
# Clean exit (0) or Ctrl-C (130) / SIGTERM (143): discard ./local/log and start fresh.
# Any other exit (panic/crash): move ./local/log aside to
#   ./local/crashlog-<timestamp>-exit<code>/   (its skywire-crash.log holds the
#   full traceback, thanks to runtime/debug.SetCrashOutput in storeLog()).
#
# Override the visor flags without editing this file:
#   VISOR_FLAGS="-sl debug -q http" ./dev-visor-loop.sh
#
# Runs from the repo root regardless of where it is invoked.
set -o pipefail
cd "$(dirname "$0")/.." || exit 1 # script lives in scripts/, build from repo root

VISOR_FLAGS="${VISOR_FLAGS:--sl debug -q http}"

# `go install` drops the binary in GOBIN (or GOPATH/bin) instead of the repo
# root, so the source tree stays clean and each install is an atomic rename — a
# running visor keeps its old inode, unlike `go build .`'s in-place overwrite.
# Resolve the path explicitly rather than relying on PATH.
SKYWIRE_BIN="$(go env GOBIN)"
[ -z "$SKYWIRE_BIN" ] && SKYWIRE_BIN="$(go env GOPATH)/bin"
SKYWIRE_BIN="$SKYWIRE_BIN/skywire"

while true; do
	echo ">>> building…"
	if ! time go install .; then
		echo ">>> build failed — not starting a stale binary; retrying in 5s"
		sleep 5
		continue
	fi

	# shellcheck disable=SC2086 # VISOR_FLAGS is intentionally word-split
	"$SKYWIRE_BIN" visor $VISOR_FLAGS
	code=$?

	case "$code" in
		0 | 130 | 143) # clean exit / Ctrl-C (SIGINT) / SIGTERM
			rm -rf ./local/log
			sleep 5
			clear
			;;
		*) # crash / nonzero — keep everything for postmortem
			ts=$(date +%Y%m%dT%H%M%S) # sortable, no colons/spaces
			dest="./local/crashlog-${ts}-exit${code}"
			mv ./local/log "$dest" 2>/dev/null
			echo ">>> visor exited ${code} — logs saved to ${dest}/"
			echo ">>> traceback (if any) is in ${dest}/skywire-crash.log"
			sleep 10 # no clear: leave the terminal trace on screen
			;;
	esac
done
