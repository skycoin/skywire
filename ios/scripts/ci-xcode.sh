#!/usr/bin/env bash
# CI helpers for the iOS lanes (ios-app.yml, ios-release.yml), kept here so
# both workflows run the same commands and a maintainer can run them too.
#
#   ci-xcode.sh select       select the newest Xcode 27 on the machine, or fail
#                            naming the ones there are
#   ci-xcode.sh destination  print an xcodebuild destination: an iPhone on the
#                            newest iOS Simulator runtime installed
set -euo pipefail

case "${1:-}" in
select)
  # The project is Xcode 27's (objectVersion 77, the iOS 27 SDK, Swift 6.4).
  xcode=$(ls -d /Applications/Xcode_27*.app /Applications/Xcode-27*.app 2>/dev/null | sort -V | tail -1 || true)
  if [ -z "$xcode" ] && xcodebuild -version 2>/dev/null | grep -q '^Xcode 27'; then
    xcode=$(dirname "$(dirname "$(xcode-select -p)")")
  fi
  if [ -z "$xcode" ]; then
    echo "::error::no Xcode 27 here; installed: $(ls -d /Applications/Xcode*.app 2>/dev/null | xargs -n1 basename | tr '\n' ' ')"
    exit 1
  fi
  sudo xcode-select -s "$xcode/Contents/Developer"
  xcodebuild -version
  ;;
destination)
  udid=$(xcrun simctl list devices available --json | jq -r '
    .devices | to_entries
    | map(select(.key | test("SimRuntime\\.iOS-")))
    | sort_by(.key | capture("iOS-(?<major>[0-9]+)-(?<minor>[0-9]+)") | [(.major | tonumber), (.minor | tonumber)])
    | map(.value | map(select(.name | startswith("iPhone"))))
    | map(select(length > 0)) | last | first | .udid // empty')
  if [ -z "$udid" ]; then
    echo "::error::no iPhone Simulator available" >&2
    xcrun simctl list devices available >&2
    exit 1
  fi
  echo "platform=iOS Simulator,id=$udid"
  ;;
*)
  echo "usage: $0 select|destination" >&2
  exit 2
  ;;
esac
