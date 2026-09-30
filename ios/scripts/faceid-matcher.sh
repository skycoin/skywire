#!/usr/bin/env bash
# Answers the booted Simulator's Face ID prompts with a match, one per
# request: SkywireUITests' FlowChecks.testAppLockRoundTrip drops a file named
# faceid-match in its runner's tmp directory whenever it wants the next prompt
# to pass. Run it in the background for that test; stop it afterwards.
# Face ID must be enrolled first:
#   xcrun simctl spawn booted notifyutil -s com.apple.BiometricKit.enrollmentChanged 1
#   xcrun simctl spawn booted notifyutil -p com.apple.BiometricKit.enrollmentChanged
set -u
runner=${1:-com.skycoin.skywire.uitests.xctrunner}
while :; do
  dir=$(xcrun simctl get_app_container booted "$runner" data 2>/dev/null)
  if [ -n "$dir" ] && [ -f "$dir/tmp/faceid-match" ]; then
    rm -f "$dir/tmp/faceid-match"
    sleep 2   # let the prompt come up
    xcrun simctl spawn booted notifyutil -p com.apple.BiometricKit_Sim.pearl.match >/dev/null
    echo "matched $(date +%T)"
  fi
  sleep 0.5
done
