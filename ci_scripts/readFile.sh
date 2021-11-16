#!/bin/bash
VAR=0
FINAL=""
while IFS= read -r line; do
    if [ "${line:0:3}" = "## " ]; then
        VAR=$(($VAR + 1))
    fi
    if [ $VAR -eq 1 ]; then
        if [ "${line}" = '' ]; then
            FINAL+="\n"
        else
            FINAL+=$line
            FINAL+="\n"
        fi
    fi
done <"$1"

echo -e $FINAL >> CACHE.md

GITHUB_TOKEN="$2" goreleaser release  --rm-dist --release-notes  ../CACHE.md

rm CACHE.md