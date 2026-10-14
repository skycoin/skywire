#!/bin/sh
# A second visor on the Mac for the iOS checks that need a peer: chat, calls,
# Skymail, Fleet and remote management (ios/gates/regression.md). It is this
# checkout's lite core with every local port moved off the Simulator app's
# (API 18000, skychat 18001, launcher 15505, ui_server 18081, desk 18010,
# SkySOCKS client 11080, SkyDEX client 18051), its API answering on loopback
# without a login, skychat keeping its history (the chat test reads it), and
# its CLI RPC on localhost:13435 for `skywire-cli --via dmsg://<phone pk>`
# (remote management). Its calls use the Mac's audio device, and its API
# reports the level of a call's audio from the phone (ios/scripts/peer-call.py).
#
#   ios/scripts/desktop-peer.sh <dir> [phone pk]
#
# Run from the repo root. Builds into <dir>/bin, writes
# <dir>/skywire-config.json the first time (kept after that, so the peer
# keeps its key), and runs the visor in the foreground; Ctrl-C stops it. A
# phone pk is written into the peer's `hypervisors`, which lists the peer on
# that phone's Fleet screen.
#
# PEER_MARKET=1 also runs a SkyDEX market beside it, the skydex-market app
# built from this checkout into <dir>/bin (the lite core does not carry it),
# with its operator UI on 127.0.0.1:18050. The market's key is the peer's pk.
set -eu

dir=${1:?usage: ios/scripts/desktop-peer.sh <dir> [phone pk]}
phone=${2:-}
mkdir -p "$dir/bin"
dir=$(cd "$dir" && pwd)

make build-mobile BUILD_PATH="$dir/bin/"
market=${PEER_MARKET:-0}
if [ "$market" = 1 ]; then
  go build -o "$dir/bin/skydex-market" ./cmd/apps/skydex-market
fi
cfg="$dir/skywire-config.json"
if [ ! -f "$cfg" ]; then
  "$dir/bin/skywire-mobile" config gen -o "$cfg" -i --nofetch
fi

python3 -I - "$cfg" "$dir" "$phone" "$market" <<'EOF'
import json, sys
path, home, phone, market = sys.argv[1:5]
c = json.load(open(path))
hv = c["hypervisor"]
hv.update(http_addr="127.0.0.1:18000", desk_addr="127.0.0.1:18010", enable_auth=False,
          db_path=f"{home}/users.db")
hv.pop("lan_dmsg_server", None)
c["ui_server"].update(enable=False, local_addr="localhost:18081")
c["launcher"].update(server_addr="localhost:15505", bin_path=f"{home}/bin")
c["local_path"] = f"{home}/local"
c["cli_addr"] = "localhost:13435"
c["dmsgscp"] = {"disabled": True}
c.pop("skywire-tcp", None)
c["hypervisors"] = [phone] if phone else []
args = {
    "skychat": f"--addr 127.0.0.1:18001 --persist --persist-db {home}/local/skychat-history.db",
    "skysocks-client": "--addr 127.0.0.1:11080",
    "skydex-client": "--addr 127.0.0.1:18051",
    "skydex-market": "--addr 127.0.0.1:18050",
}
running = {"skychat", "skymail"} | ({"skydex-market"} if market == "1" else set())
for app in c["launcher"]["apps"]:
    if app["name"] in args:
        app["args"] = args[app["name"]]
    if app["name"] == "skydex-market":
        app["binary"] = "skydex-market"
    app["auto_start"] = app["name"] in running
json.dump(c, open(path, "w"), indent=2)
EOF

echo "peer: API http://127.0.0.1:18000, skychat http://127.0.0.1:18001, pk $(python3 -I -c 'import json,sys; print(json.load(open(sys.argv[1]))["pk"])' "$cfg")"
cd "$dir"
exec ./bin/skywire-mobile visor -c "$cfg"
