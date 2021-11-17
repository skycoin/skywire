#!/bin/bash
var=0
final=""
while IFS= read -r line; do
    if [ "${line:0:3}" = "## " ]; then
        var=$(($var + 1))
    fi
    if [ $var -eq 1 ]; then
        if [ "${line}" = '' ]; then
            final+="\n"
        else
            final+=$line
            final+="\n"
        fi
    fi
done <"$1"

echo -e $final >release-notes.md

GITHUB_TOKEN="$2" goreleaser release --rm-dist --release-notes ../release-notes.md

rm release-notes.md
