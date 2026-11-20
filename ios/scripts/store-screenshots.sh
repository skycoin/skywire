#!/bin/sh
# App Store screenshots from the Simulator (playbook item 8.3): the screen
# tour in each language, on a device of a size App Store Connect takes, with
# the status bar store screenshots use (9:41, full signal and battery).
#
#   ios/scripts/store-screenshots.sh "iPhone 18 Pro Max" [en zh-Hans es fr]
#
# Run from the repo root after make ios-core, with the app's ports free
# (ios/README.md). The pictures go to ios/build/store-screenshots/<device>/
# <language>/, named by the tour's step; ios/store/screenshots.md says which
# to use. The status bar is reset afterwards.
set -eu

device=${1:?usage: ios/scripts/store-screenshots.sh <Simulator name> [languages]}
shift
languages=${*:-en zh-Hans es fr}
out="ios/build/store-screenshots/$device"
mkdir -p "$out"

xcrun simctl bootstatus "$device" -b
xcrun simctl status_bar "$device" override --time 9:41 --dataNetwork wifi --wifiMode active \
  --wifiBars 3 --cellularMode active --cellularBars 4 --batteryState charged --batteryLevel 100
trap 'xcrun simctl status_bar "$device" clear' EXIT

for language in $languages; do
  case $language in
    en) test=testTourEnglish ;;
    zh-Hans) test=testTourSimplifiedChinese ;;
    es) test=testTourSpanish ;;
    fr) test=testTourFrench ;;
    *) echo "no tour in $language (en, zh-Hans, es, fr)" >&2; exit 1 ;;
  esac
  result="$out/$language-$(date +%Y%m%d-%H%M%S).xcresult"
  xcodebuild -project ios/Skywire.xcodeproj -scheme SkywireTour \
    -destination "platform=iOS Simulator,name=$device" -derivedDataPath ios/DerivedData \
    -resultBundlePath "$result" test -only-testing:"SkywireUITests/ScreenTour/$test"
  xcrun xcresulttool export attachments --path "$result" --output-path "$out/$language"
done
echo "screenshots in $out"
