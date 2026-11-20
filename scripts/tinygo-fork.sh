#!/bin/sh
# Print the tinygo binary the TinyGo make targets should run.
#
# On linux/amd64 this keeps a copy of the 0magnet/tinygo fork's "toolchain"
# release in $TINYGO_FORK_DIR (default ~/.local/share/tinygo-skywire) and
# updates it whenever the release checksum changes. The fork's CI rebuilds that
# release on every push to its develop branch.
#
# Elsewhere, under CI, with GOPROXY=off, or with TINYGO_FORK=0 it prints the
# tinygo on PATH. Offline it keeps using the copy it already has.
set -u

say() { echo "tinygo-fork: $*" >&2; }

dir=${TINYGO_FORK_DIR:-$HOME/.local/share/tinygo-skywire}
url=https://github.com/0magnet/tinygo/releases/download/toolchain
asset=tinygo.linux-amd64.tar.gz

use_path() {
	if [ -x "$dir/bin/tinygo" ]; then
		echo "$dir/bin/tinygo"
	else
		command -v tinygo || echo tinygo
	fi
	exit 0
}

# fetch NAME MAXTIME OUT downloads a release asset. The download URL can
# return 500 for a while after the release is recreated, so it falls back to
# the API, which serves the same file.
fetch() {
	curl -fsSL --max-time "$2" -o "$3" "$url/$1" 2>/dev/null && return 0
	api=$(curl -fsSL --max-time 20 https://api.github.com/repos/0magnet/tinygo/releases/tags/toolchain 2>/dev/null |
		tr ',' '\n' | awk -v n="\"name\": \"$1\"" '
			/"url": "https:\/\/api.github.com\/repos\/.*\/releases\/assets\// { u = $0 }
			index($0, n) { print u; exit }' | sed 's/.*"url": "\([^"]*\)".*/\1/')
	[ -n "$api" ] && curl -fsSL --max-time "$2" -H 'Accept: application/octet-stream' -o "$3" "$api" 2>/dev/null
}

# CI tests against the upstream TinyGo release it installs.
[ -n "${CI:-}" ] && TINYGO_FORK=${TINYGO_FORK:-0}
# A build with the module proxy off downloads nothing, a toolchain included.
[ "${GOPROXY:-}" = off ] && TINYGO_FORK=${TINYGO_FORK:-0}
[ "${TINYGO_FORK:-1}" = 0 ] && { command -v tinygo || echo tinygo; exit 0; }
[ "$(uname -s)/$(uname -m)" = Linux/x86_64 ] || use_path

want=$(fetch "$asset.sha256" 20 - | cut -d' ' -f1)
if [ -z "$want" ]; then
	say "cannot reach the toolchain release, using the installed tinygo"
	use_path
fi
if [ "$want" = "$(cat "$dir/.sha256" 2>/dev/null)" ] && [ -x "$dir/bin/tinygo" ]; then
	echo "$dir/bin/tinygo"
	exit 0
fi

say "updating $dir to the latest toolchain release"
tmp=$(mktemp -d "${dir%/*}/.tinygo-fork.XXXXXX" 2>/dev/null) || {
	mkdir -p "${dir%/*}" && tmp=$(mktemp -d "${dir%/*}/.tinygo-fork.XXXXXX")
} || use_path
trap 'rm -rf "$tmp"' EXIT

if ! fetch "$asset" 900 "$tmp/$asset"; then
	say "download failed, using the installed tinygo"
	use_path
fi
if [ "$(sha256sum "$tmp/$asset" | cut -d' ' -f1)" != "$want" ]; then
	say "checksum mismatch, using the installed tinygo"
	use_path
fi
tar -xzf "$tmp/$asset" -C "$tmp" || use_path
echo "$want" >"$tmp/tinygo/.sha256"

rm -rf "$dir.old"
[ -d "$dir" ] && mv "$dir" "$dir.old"
mv "$tmp/tinygo" "$dir"
rm -rf "$dir.old"
say "$("$dir/bin/tinygo" version)"
echo "$dir/bin/tinygo"
