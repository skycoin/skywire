#!/bin/sh
# wasm-module.sh <skywire.wasm> <outdir> <version>
#
# Writes the js/wasm module as a visor serves it (skywire.wasm.gz) and its
# manifest (skywire.wasm.json) into outdir. A visor compares the manifest's
# sha256 with the one its source serves to tell whether its copy is current.
set -eu
in=$1 out=$2 ver=$3
mkdir -p "$out"
gzip -9 -n -c "$in" > "$out/skywire.wasm.gz"
rev=$(strings -a "$in" | grep -oE 'vcs\.revision=[0-9a-f]{40}' | head -1 | cut -d= -f2)
if [ -z "$rev" ]; then
	echo "wasm-module.sh: no vcs.revision in $in" >&2
	exit 1
fi
sum=$(sha256sum "$out/skywire.wasm.gz" | cut -d' ' -f1)
size=$(wc -c < "$out/skywire.wasm.gz" | tr -d ' ')
printf '{"version":"%s","revision":"%s","sha256":"%s","size":%s}\n' "$ver" "$rev" "$sum" "$size" > "$out/skywire.wasm.json"
cat "$out/skywire.wasm.json"
