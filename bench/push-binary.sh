#!/bin/sh
# push-binary.sh — build here, push straight to rig visors, restart them. Minutes, not a
# fleet publish.
#
#   bench/push-binary.sh [-b <binary>] <pk> [<pk>...]
#
# Without -b the repo is built first, stripped (`go build -trimpath -ldflags "-s -w" .`),
# into $TMPDIR, never into the checkout. The binary is gzipped before it goes over
# `skywire cli dmsg scp` (it lands in the visor's dmsgscp root, <local_path>/scp-root by
# default), which roughly halves the transfer. Each target then gets a `pty exec` that
# unpacks it, installs it over the path the unit's ExecStart names and restarts the unit.
# Several targets are pushed in parallel. Prints the version each visor reports afterwards.
# The visors' auto-updaters are left alone — stop them yourself for a frozen rig
# (`systemctl stop skywire-update.timer` on each).
set -u
CLI=${CLI:-/home/d0mo/go/bin/skywire}
here=$(dirname "$0"); repo=$(cd "$here/.." && pwd)
tmp=$(mktemp -d "${TMPDIR:-/tmp}/push-binary.XXXXXX") || exit 1
trap 'rm -rf "$tmp"' EXIT
bin=""
[ "${1:-}" = -b ] && { bin=$2; shift 2; }
[ $# -ge 1 ] || { echo "usage: $0 [-b <binary>] <pk>..."; exit 2; }
if [ -z "$bin" ]; then
	(cd "$repo" && go build -trimpath -ldflags "-s -w" -o "$tmp/skywire" .) || exit 1
	bin="$tmp/skywire"
fi
ver=$("$bin" -v 2>/dev/null | head -1); echo "pushing $bin ($ver)"
gzip -1 -c "$bin" > "$tmp/skywire.gz" || exit 1
echo "compressed $(wc -c < "$bin") -> $(wc -c < "$tmp/skywire.gz") bytes"

# remote_install: find the unit's binary path and config, unpack the upload, move it into
# place, restart
remote_install='
unit=$(systemctl show -p ExecStart --value skywire | grep -o "path=[^ ;]*" | cut -d= -f2)
unit=$(readlink -f "$unit") # the unit may name a symlink (/usr/bin/skywire -> /opt/skywire/bin/skywire); install over the target
cfg=$(systemctl show -p ExecStart --value skywire | grep -o "\-c [^ ;]*" | awk "{print \$2}")
[ -n "$cfg" ] || cfg=/opt/skywire/skywire.json
lp=$(grep -o "\"local_path\": *\"[^\"]*\"" "$cfg" | sed "s/.*: *\"//; s/\"$//")
[ -n "$lp" ] || lp=$(dirname "$cfg")/local
f="$lp/scp-root/skywire.new"
[ -s "$f.gz" ] || { echo "upload not found at $f.gz"; exit 1; }
gunzip -f "$f.gz" || { echo "gunzip failed"; exit 1; }
install -m755 "$f" "$unit" && rm -f "$f" && systemctl restart skywire && sleep 4 && echo "installed $unit: $(skywire -v | head -1)"
'
push_one() {
	pk=$1
	up=$(timeout 900 $CLI cli dmsg scp --transport "${SCP_TRANSPORT:-skynet}" "$tmp/skywire.gz" "$pk:skywire.new.gz" 2>&1 | grep -v DEBUG | tail -1)
	inst=$(timeout 120 $CLI cli pty exec "$pk" --timeout 90s -- /bin/sh -c "$remote_install" 2>&1 | grep -v DEBUG | tail -2)
	printf '== %s\n%s\n%s\n' "$pk" "$up" "$inst"
}
for pk in "$@"; do
	push_one "$pk" &
done
wait
