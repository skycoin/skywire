#!/usr/bin/env bash
# run-visor.sh — restart loop for the working-tree visor with log
# capture to /tmp/skywire-visor.log so the panic / startup output
# survives between iterations (the original loop's `clear` wipes it).
#
# Run from the working tree: ./run-visor.sh
#
# Each iteration:
#   1. git pull --ff-only — pick up upstream commits
#   2. rm -rf ./local/log — clean per-restart log dir
#   3. go build . — rebuild the visor binary
#   4. mark a timestamp in the log file
#   5. run the visor under `script -fq` which gives it a real pty
#      (preserves logrus color output the way the operator sees it
#      in their interactive shell — plain tee fools isatty()
#      checks for some logger paths and drops color).
#   6. on exit (crash or operator halt), sleep 5 and loop
#
# Read the log later with `less -R` to render the ANSI color
# codes, or `cat`/`tail` for plain text (codes will look like
# `^[[33m`).

set -u

LOG=/tmp/skywire-visor.log

while true ; do
	rm -rf ./local/log
	git pull --ff-only 2>&1 | head -2
	go build .
	printf '\n=== %s restart ===\n' "$(date -u +%FT%TZ)" >> "$LOG"
	script -qfa "$LOG" -c "./skywire visor -sl debug -q http"
	sleep 5
	clear
done
