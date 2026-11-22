#!/usr/bin/env python3
"""The footprint table of playbook item 8.2, measured on a Simulator.

Runs FootprintTour (SkywireUITests, scheme SkywireTour), which holds the app
in one state per test: idle, connected, chat open, SkySOCKS active, SkyDEX
open, in a call. Meanwhile this samples the app every 15 s with the Mac's
footprint(1) and prints one row per state: the median and the highest
phys_footprint, and the median of Go's share. Go's share is G1's split, the
Untagged category (the Go runtime maps its heap, stacks and metadata
anonymously) plus the dirty __DATA of the image that holds the core. The
first minute of each hold is left out, so a row is the state, not its start.

  ios/scripts/footprint-table.py --device "iPhone 18 Pro" [--hold 240]
      [--call-peer http://127.0.0.1:18000] [--dex-market <pk>]

From the repo root, after make ios-core, with nothing else on the app's ports
(ios/README.md). The connected state holds at least 10 minutes, G1's soak.
While SkySOCKS holds, each sample also fetches https://api.ipify.org through
the proxy. The call row needs a second visor on the Mac whose API answers
without a login (--call-peer); it calls this phone, and the tour answers.
Without it the row is skipped, as is the trading page without --dex-market
(the DEX row is then the screen after a failed dial). Samples, the
xcodebuild log and the result bundle go to ios/build/footprint-<time>/.
"""
import argparse
import json
import os
import re
import statistics
import subprocess
import sys
import time
import urllib.request

ROOT = os.path.abspath(os.path.join(os.path.dirname(__file__), "..", ".."))
BUNDLE = "com.skycoin.skywire"
EVERY = 15
SETTLE = 60


def udid_of(name):
    devices = json.loads(subprocess.check_output(["xcrun", "simctl", "list", "devices", "available", "-j"]))["devices"]
    found = [d["udid"] for runtime in devices.values() for d in runtime if d["name"] == name]
    if len(found) != 1:
        sys.exit(f"{len(found)} available Simulators are named {name!r}; name exactly one")
    return found[0]


def app_pid(udid):
    out = subprocess.run(["pgrep", "-f", f"Devices/{udid}/.*/Skywire\\.app/Skywire( |$)"],
                         capture_output=True, text=True).stdout.split()
    return int(out[-1]) if out else None


def footprint(pid):
    """phys_footprint, the Untagged category's dirty bytes, and the dirty
    __DATA of the image the core is linked into (the app binary, or
    Skywire.debug.dylib in a Debug build). The Simulator's vmmap hangs on the
    app; footprint -v has the same split."""
    out = subprocess.run(["footprint", "-p", str(pid), "-v", "-f", "bytes"], capture_output=True, text=True).stdout
    phys = re.search(r"phys_footprint:\s+(\d+) B", out)
    untagged = re.search(r"^\s*(\d+) B\s+\d+ B\s+\d+ B\s+\d+\s+Untagged$", out, re.M)
    core = re.findall(r"^\s*(\d+) B\s+\d+ B\s+\d+ B\s+\d+\s+__DATA \.\.\./Skywire(?:\.debug\.dylib)?$", out, re.M)
    if not phys:
        return None
    return int(phys.group(1)), int(untagged.group(1)) if untagged else 0, sum(map(int, core))


def app_pk(udid):
    container = subprocess.check_output(["xcrun", "simctl", "get_app_container", udid, BUNDLE, "data"], text=True).strip()
    with open(os.path.join(container, "Library", "Application Support", "skywire", "skywire-config.json")) as f:
        return json.load(f)["pk"]


def call(peer, callee):
    """Asks the peer's visor to call `callee`; the dial returns once it rings."""
    with urllib.request.urlopen(peer + "/api/about", timeout=10) as r:
        caller = json.load(r)["public_key"]
    with urllib.request.urlopen(peer + "/api/csrf", timeout=10) as r:
        token = json.load(r)["csrf_token"]
    request = urllib.request.Request(f"{peer}/api/visors/{caller}/skychat/voice/call",
                                     data=json.dumps({"peer": callee}).encode(), method="POST",
                                     headers={"Content-Type": "application/json", "X-CSRF-Token": token})
    with urllib.request.urlopen(request, timeout=95) as r:
        return r.read().decode()


def through_proxy():
    result = subprocess.run(["curl", "-s", "-m", "20", "--socks5-hostname", "127.0.0.1:1080", "https://api.ipify.org"],
                            capture_output=True, text=True)
    return result.stdout.strip() or f"curl exit {result.returncode}"


def mib(value):
    return f"{value / (1 << 20):.1f}"


def main():
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    parser.add_argument("--device", required=True, help="the Simulator's name")
    parser.add_argument("--hold", type=int, default=240, help="seconds per state (connected holds at least 600)")
    parser.add_argument("--call-peer", help="a visor's API origin on the Mac, without login, to call the phone")
    parser.add_argument("--dex-market", help="a SkyDEX market's public key")
    args = parser.parse_args()

    udid = udid_of(args.device)
    out = os.path.join(ROOT, "ios", "build", "footprint-" + time.strftime("%Y%m%d-%H%M%S"))
    os.makedirs(out)
    state_file = os.path.join(out, "state")
    with open(state_file, "w") as f:
        f.write("-\n")

    env = dict(os.environ,
               TEST_RUNNER_SKYWIRE_FOOTPRINT_STATE=state_file,
               TEST_RUNNER_SKYWIRE_FOOTPRINT_HOLD=str(args.hold))
    if args.call_peer:
        env["TEST_RUNNER_SKYWIRE_FOOTPRINT_CALL"] = "1"
    if args.dex_market:
        env["TEST_RUNNER_SKYWIRE_DEX_MARKET"] = args.dex_market
    log = open(os.path.join(out, "xcodebuild.log"), "w")
    tour = subprocess.Popen(
        ["xcodebuild", "-project", "ios/Skywire.xcodeproj", "-scheme", "SkywireTour",
         "-destination", f"platform=iOS Simulator,id={udid}", "-derivedDataPath", "ios/DerivedData",
         "-resultBundlePath", os.path.join(out, "Footprint.xcresult"),
         "test", "-only-testing:SkywireUITests/FootprintTour"],
        cwd=ROOT, env=env, stdout=log, stderr=subprocess.STDOUT)
    print(f"FootprintTour on {args.device} ({udid}); samples in {out}", flush=True)

    samples = open(os.path.join(out, "samples.tsv"), "w")
    samples.write("time\tstate\tpid\tphys_footprint\tuntagged_dirty\tcore_data_dirty\tnote\n")
    started, rows, called = {}, {}, False
    while tour.poll() is None:
        time.sleep(EVERY)
        with open(state_file) as f:
            state = f.read().strip()
        if state == "call-ring" and not called:
            try:
                print("calling the phone:", call(args.call_peer, app_pk(udid)), flush=True)
                called = True
            except Exception as error:  # the peer may not reach a new identity yet: try again
                print("call failed, retrying:", error, flush=True)
            continue
        if state in ("-", "", "call-ring"):
            continue
        now = time.time()
        if now - started.setdefault(state, now) < SETTLE:
            continue
        pid = app_pid(udid)
        note = through_proxy() if state == "socks" else ""
        measured = footprint(pid) if pid else None
        if not measured:
            continue
        phys, untagged, core = measured
        rows.setdefault(state, []).append((phys, untagged + core))
        samples.write(f"{time.strftime('%H:%M:%S')}\t{state}\t{pid}\t{phys}\t{untagged}\t{core}\t{note}\n")
        samples.flush()

    print(f"xcodebuild exited {tour.returncode}; see {out}/xcodebuild.log")
    table = ["| state | samples | phys_footprint median (MiB) | highest (MiB) | Go's share median (MiB) |",
             "|---|---|---|---|---|"]
    for state, values in rows.items():
        phys = [p for p, _ in values]
        go = [g for _, g in values]
        table.append(f"| {state} | {len(values)} | {mib(statistics.median(phys))} | {mib(max(phys))} | {mib(statistics.median(go))} |")
    with open(os.path.join(out, "table.md"), "w") as f:
        f.write("\n".join(table) + "\n")
    print("\n".join(table))
    return tour.returncode


if __name__ == "__main__":
    sys.exit(main())
