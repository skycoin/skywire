#!/usr/bin/env bash
# Answers the booted Simulator's Face ID prompts with a match while
# SkywireUITests asks: FlowChecks.testAppLockRoundTrip keeps a file named
# faceid-match in its runner's tmp directory for as long as a step wants its
# prompts to pass, and removes it when the step is done. The Simulator drops
# a match sent while no prompt is up, so this keeps sending until the file
# is gone (one match per request lost itself between two sheets). Run it in
# the background for that test; stop it afterwards.
# Face ID must be enrolled first:
#   xcrun simctl spawn booted notifyutil -s com.apple.BiometricKit.enrollmentChanged 1
#   xcrun simctl spawn booted notifyutil -p com.apple.BiometricKit.enrollmentChanged
set -u
runner=${1:-com.skycoin.skywire.uitests.xctrunner}
while :; do
  dir=$(xcrun simctl get_app_container booted "$runner" data 2>/dev/null)
  if [ -n "$dir" ] && [ -f "$dir/tmp/faceid-match" ]; then
    xcrun simctl spawn booted notifyutil -p com.apple.BiometricKit_Sim.pearl.match >/dev/null
    echo "matched $(date +%T)"
    sleep 1
  fi
  sleep 0.5
done
