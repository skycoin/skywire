#!/bin/sh
# Run the hot-path benchmarks under Go and TinyGo and print them side by side.
#
#   sh scripts/bench-compare.sh            # every benchmark below
#   sh scripts/bench-compare.sh cxo        # only lines matching a pattern
#
# TINYGO picks the compiler (default tinygo), TGFLAGS its flags (default the
# release build's), BENCHTIME the -benchtime of both runs (default 1s).
# The safe tag keeps testify's copy of go-spew out of reflect.Value's
# internals, which TinyGo lays out differently.
set -u

TINYGO=${TINYGO:-tinygo}
TGFLAGS=${TGFLAGS:--gc=boehm -scheduler=cores -stack-size=1MB -tags withoutsystray,safe}
BENCHTIME=${BENCHTIME:-1s}
filter=${1:-.}

# package  benchmark
benches='
./pkg/transport           MakeTransportID
./pkg/transport           SortEdges
./pkg/dmsg/noise          ReadWriterFrame
./pkg/dmsg/dmsg           YamuxStream
./pkg/cxo/node/msg        ObjectEncode
./pkg/cxo/node/msg        ObjectDecode
./pkg/cxo/node/transport  ConnectionSend
./pkg/cipher              VerifyPubKeySignedPayload
'

out=$(mktemp -d)
trap 'rm -r "$out"' EXIT
: >"$out/go"
: >"$out/tinygo"

echo "$benches" | awk 'NF==2' | grep -E -- "$filter" | while read -r pkg name; do
	go test -run '^$' -bench "^Benchmark$name\$" -benchmem -benchtime="$BENCHTIME" "$pkg" >>"$out/go" 2>&1 ||
		echo "go: $pkg $name failed" >&2
	# shellcheck disable=SC2086
	$TINYGO test $TGFLAGS -run '^$' -bench "^Benchmark$name\$" -benchmem -benchtime="$BENCHTIME" "$pkg" >>"$out/tinygo" 2>&1 ||
		echo "tinygo: $pkg $name failed" >&2
done

# Benchmark lines carry name-N, iterations, then value unit pairs.
awk '
function field(unit,    i) {
	for (i = 3; i < NF; i += 2) if ($(i+1) == unit) return $i
	return "-"
}
/^Benchmark/ {
	name = $1; sub(/-[0-9]+$/, "", name); sub(/^Benchmark/, "", name)
	if (FILENAME ~ /go$/ && FILENAME !~ /tinygo$/) { gns[name] = field("ns/op"); gb[name] = field("B/op"); gal[name] = field("allocs/op"); order[++n] = name }
	else { tns[name] = field("ns/op"); tb[name] = field("B/op"); tal[name] = field("allocs/op") }
}
END {
	printf "%-26s %11s %12s %6s %9s %9s %9s %9s\n", "benchmark", "go ns/op", "tinygo ns/op", "ratio", "go B", "tg B", "go allocs", "tg allocs"
	for (i = 1; i <= n; i++) {
		k = order[i]; t = (k in tns) ? tns[k] : "-"
		r = (t != "-" && gns[k] > 0) ? sprintf("%.2f", t / gns[k]) : "-"
		printf "%-26s %11s %12s %6s %9s %9s %9s %9s\n", k, gns[k], t, r, gb[k], (k in tb) ? tb[k] : "-", gal[k], (k in tal) ? tal[k] : "-"
	}
}' "$out/go" "$out/tinygo"
