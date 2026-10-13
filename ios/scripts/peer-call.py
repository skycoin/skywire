#!/usr/bin/env python3
"""Drives a voice call between the desktop peer and the phone, for the call
steps of ios/gates/regression.md. The peer is ios/scripts/desktop-peer.sh's
visor (API on 127.0.0.1:18000, no login); its voice bridge hands the call's
audio to its API, so the level it receives from the phone is the check that
audio crossed.

  ios/scripts/peer-call.py dial <phone pk>     # rings the phone, prints the call id
  ios/scripts/peer-call.py incoming            # ids of calls ringing on the peer (the phone dialled it)
  ios/scripts/peer-call.py answer <call id>    # the peer answers the phone's call
  ios/scripts/peer-call.py levels <call id> [seconds]   # once a second: recv, the phone's audio
  ios/scripts/peer-call.py hangup <call id>

recv is the RMS of the last 0.1 s the peer received, 0 to 1. The Simulator's
microphone is the Mac's: a quiet room reads around 0.001 to 0.01, a voice
higher, and a muted phone 0.
"""
import http.cookiejar
import json
import sys
import time
import urllib.error
import urllib.request

PEER = "http://127.0.0.1:18000"
opener = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()))


def get(path):
    with opener.open(PEER + path, timeout=30) as r:
        return json.load(r)


def post(path, body):
    token = get("/api/csrf")["csrf_token"]
    request = urllib.request.Request(PEER + path, data=json.dumps(body).encode(), method="POST",
                                     headers={"Content-Type": "application/json", "X-CSRF-Token": token})
    with opener.open(request, timeout=95) as r:
        return json.load(r)


def main():
    if len(sys.argv) < 2:
        sys.exit(__doc__)
    verb, args = sys.argv[1], sys.argv[2:]
    voice = f"/api/visors/{get('/api/about')['public_key']}/skychat/voice"
    if verb == "dial":
        print(post(voice + "/call", {"peer": args[0]})["call_id"])
    elif verb == "incoming":
        # Each entry reads "<call id> from <caller pk>".
        for entry in get(voice + "/incoming"):
            print(entry.split()[0])
    elif verb == "answer":
        print(json.dumps(post(voice + "/answer", {"call_id": args[0]})))
    elif verb == "levels":
        for _ in range(int(args[1]) if len(args) > 1 else 10):
            level = get(f"{voice}/levels?call={args[0]}")
            print(f"{time.strftime('%H:%M:%S')} recv {level['recv']:.4f} sent {level['sent']:.4f}", flush=True)
            time.sleep(1)
    elif verb == "hangup":
        print(json.dumps(post(voice + "/hangup", {"call_id": args[0]})))
    else:
        sys.exit(__doc__)


if __name__ == "__main__":
    try:
        main()
    except urllib.error.HTTPError as error:
        sys.exit(f"{error.code}: {error.read().decode(errors='replace').strip()}")
