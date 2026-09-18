#!/bin/sh
# bench.sh — one certified transfer through a SOCKS5 proxy session, one TSV row.
#
#   bench/bench.sh <socks host:port> <sink base url> <bytes> <down|up> [label]
#
# down: GET <sink>/?bytes=N, verify sha256 against the sink's X-Sha256.
# up:   POST N deterministic bytes to <sink>/upload, verify the sink's sha256
#       of what it received against ours.
# Row: label dir bytes speed_Bps http got time_s hash_ok
#
# BENCH_FAIL_PREFIX: where a NON-2xx answer is kept. The temp dir goes with the
# row, so without this the only surviving trace of a failed transfer is the
# status code — which is how two 502s of the 2026-09-16 compose set ended up
# undiagnosable. Set it to "<out>/<set>.row<N>" (the same stem lib-blackout.sh
# uses) and the response lands in $BENCH_FAIL_PREFIX.r.hdr / .r.body; the
# skysocks 502 names its own cause in X-Upload-Error.
set -u
socks=$1; sink=$2; n=$3; dir=$4; label=${5:-}
t=$(mktemp -d); trap 'rm -rf "$t"' EXIT
case $dir in
down)
  w=$(curl -s --socks5-hostname "$socks" -m 600 -D "$t/r.hdr" -o "$t/r.body" -w '%{http_code} %{size_download} %{time_total} %{speed_download}' "$sink/?bytes=$n")
  want=$(tr -d '\r' < "$t/r.hdr" | awk 'tolower($1)=="x-sha256:"{print $2}')
  have=$(sha256sum "$t/r.body" | cut -d' ' -f1)
  ;;
up)
  head -c "$n" /dev/urandom > "$t/b"
  have=$(sha256sum "$t/b" | cut -d' ' -f1)
  w=$(curl -s --socks5-hostname "$socks" -m 600 -D "$t/r.hdr" -o "$t/r.body" -w '%{http_code} %{size_upload} %{time_total} %{speed_upload}' -X POST --data-binary "@$t/b" "$sink/upload")
  want=$(jq -r .sha256 "$t/r.body" 2>/dev/null)
  ;;
*) echo "dir must be down|up" >&2; exit 2;;
esac
set -- $w
# Keep the answer of anything that is not a 2xx, before the temp dir goes. The
# body is truncated: it is a reason, not an artifact.
case ${1:-000} in
2??) ;;
*)
  if [ -n "${BENCH_FAIL_PREFIX:-}" ]; then
    cp "$t/r.hdr" "$BENCH_FAIL_PREFIX.r.hdr" 2>/dev/null || true
    head -c 8192 "$t/r.body" > "$BENCH_FAIL_PREFIX.r.body" 2>/dev/null || true
  fi
  ;;
esac
ok=0; [ -n "$want" ] && [ "$want" = "$have" ] && ok=1
printf '%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n' "$label" "$dir" "$n" "${4:-0}" "${1:-000}" "${2:-0}" "${3:-0}" "$ok"
