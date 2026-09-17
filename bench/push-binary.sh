#!/bin/sh
# push-binary.sh — build here, push straight to rig visors, restart them. Minutes, not a
# fleet publish.
#
#   bench/push-binary.sh [-b <binary>] <pk> [<pk>...]
#
# Without -b the repo is built first (`go build .` → ./skywire). Each target receives the
# file over `skywire cli dmsg scp` (it lands in the visor's dmsgscp root, <local_path>/scp-root
# by default), then `pty exec` installs it over the path the unit's ExecStart names and
# restarts the unit. Prints the version each visor reports afterwards. The visors'
# auto-updaters are left alone — stop them yourself for a frozen rig
# (`systemctl stop skywire-update.timer` on each).
set -u
CLI=${CLI:-/home/d0mo/go/bin/skywire}
here=$(dirname "$0"); repo=$(cd "$here/.." && pwd)
bin=""
[ "${1:-}" = -b ] && { bin=$2; shift 2; }
[ $# -ge 1 ] || { echo "usage: $0 [-b <binary>] <pk>..."; exit 2; }
if [ -z "$bin" ]; then
	(cd "$repo" && go build .) || exit 1
	bin="$repo/skywire"
fi
ver=$("$bin" -v 2>/dev/null | head -1); echo "pushing $bin ($ver)"

# remote_install: find the unit's binary path and config, move the upload into place, restart
remote_install='
unit=$(systemctl show -p ExecStart --value skywire | grep -o "path=[^ ;]*" | cut -d= -f2)
unit=$(readlink -f "$unit") # the unit may name a symlink (/usr/bin/skywire -> /opt/skywire/bin/skywire); install over the target
cfg=$(systemctl show -p ExecStart --value skywire | grep -o "\-c [^ ;]*" | awk "{print \$2}")
[ -n "$cfg" ] || cfg=/opt/skywire/skywire.json
lp=$(grep -o "\"local_path\": *\"[^\"]*\"" "$cfg" | sed "s/.*: *\"//; s/\"$//")
[ -n "$lp" ] || lp=$(dirname "$cfg")/local
f="$lp/scp-root/skywire.new"
[ -s "$f" ] || { echo "upload not found at $f"; exit 1; }
install -m755 "$f" "$unit" && rm -f "$f" && systemctl restart skywire && sleep 4 && echo "installed $unit: $(skywire -v | head -1)"
'
for pk in "$@"; do
	echo "== $pk"
	timeout 900 $CLI cli dmsg scp --transport "${SCP_TRANSPORT:-skynet}" "$bin" "$pk:skywire.new" 2>&1 | grep -v DEBUG | tail -1
	timeout 120 $CLI cli pty exec "$pk" --timeout 90s -- /bin/sh -c "$remote_install" 2>&1 | grep -v DEBUG | tail -2
done
