#!/bin/sh
# Copies the skycoin-web wallet source into the hypervisor dashboard, which
# compiles it as its wallet route. The source comes from the skycoin module at
# the version go.mod requires, so the wallet the dashboard shows and the skycoin
# code the visor runs move together, and a skycoin bump is the wallet update.
#
# The copy is generated and ignored by git: run it before building or testing
# the dashboard (the npm build and test scripts do).
set -eu

root=$(cd "$(dirname "$0")/.." && pwd)
dest="$root/static/skywire-manager-src/src/skycoin-wallet"

mod=$(cd "$root" && go mod download -json github.com/skycoin/skycoin | jq -r .Dir)
src="$mod/src/skycoin-web/src"
if [ ! -d "$src/app" ]; then
	echo "no skycoin-web source in $mod" >&2
	exit 1
fi

rm -rf "$dest"
mkdir -p "$dest/environments"
cp -R "$src/app" "$src/assets" "$src/theme" "$dest/"
cp "$src/wallet-styles.scss" "$src"/_wallet-*.scss "$dest/"
# The standalone build swaps in the production environment; the dashboard has
# no file replacement for the wallet, so the copy takes it directly.
cp "$src/environments/environment.prod.ts" "$dest/environments/environment.ts"
# The module cache is read-only, and so is everything copied out of it.
chmod -R u+w "$dest"
# The wallet's own tests need its standalone harness, not the dashboard's.
find "$dest" -name '*.spec.ts' -delete
rm -f "$dest/app/utils/wasm-test-utils.ts"
echo "skycoin-web wallet synced from $mod"
