#!/usr/bin/env bash
# The wallet cross-check (playbook item 5.4): the Android wallet-core and its
# Swift port derive and sign the same things from the same seeds.
#
# Runs the Kotlin CrossCheckDump test (android/wallet-core) and the Swift
# CrossCheckDumpTests (ios/Packages/WalletCore), each writing, for three
# fixed seeds, the addresses SKY/BTC/ETH derive and one fully signed
# transaction per chain (plus an ERC-20 transfer); then diffs the two files.
# Every signature is RFC 6979, so identical means identical bytes.
#
# Usage: ios/scripts/wallet-crosscheck.sh [output-dir]
#   Leaves kotlin.txt and swift.txt (and each side's build log) in output-dir
#   (default: a temporary directory, removed on exit). Exit 0 and
#   "identical" when they match.
#
# Needs: a JDK 17+ for Gradle (JAVA_HOME; defaults to Android Studio's bundled
# one when unset), Xcode's swift. Offline: no node is contacted.
set -euo pipefail

ROOT=$(cd "$(dirname "$0")/../.." && pwd)

if [ -n "${1:-}" ]; then
  out=$(mkdir -p "$1" && cd "$1" && pwd)
else
  out=$(mktemp -d)
  trap 'rm -rf "$out"' EXIT
fi
rm -f "$out/kotlin.txt" "$out/swift.txt"

if [ -z "${JAVA_HOME:-}" ] && [ -d "/Applications/Android Studio.app/Contents/jbr/Contents/Home" ]; then
  export JAVA_HOME="/Applications/Android Studio.app/Contents/jbr/Contents/Home"
fi

# Each side's build and test output goes to a log, shown only on failure.
echo "Kotlin (android/wallet-core)…"
if ! (cd "$ROOT/android" && WALLET_CROSSCHECK_OUT="$out/kotlin.txt" \
  ./gradlew -q :wallet-core:test --tests 'com.skycoin.wallet.CrossCheckDump' --rerun) >"$out/kotlin.log" 2>&1; then
  cat "$out/kotlin.log" >&2
  exit 1
fi

echo "Swift (ios/Packages/WalletCore)…"
if ! (cd "$ROOT/ios/Packages/WalletCore" && WALLET_CROSSCHECK_OUT="$out/swift.txt" \
  swift test --filter CrossCheckDumpTests) >"$out/swift.log" 2>&1; then
  cat "$out/swift.log" >&2
  exit 1
fi

for f in kotlin.txt swift.txt; do
  if [ ! -s "$out/$f" ]; then
    echo "$f was not written: its dump test was skipped or failed" >&2
    exit 1
  fi
done

if diff -u "$out/kotlin.txt" "$out/swift.txt"; then
  echo "identical: $(wc -l <"$out/kotlin.txt" | tr -d ' ') lines" \
    "($(grep -c ' tx: ' "$out/kotlin.txt") signed transactions, 3 seeds)"
else
  echo "the Kotlin and Swift wallets differ (diff above)" >&2
  exit 1
fi
