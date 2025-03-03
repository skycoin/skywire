#!/usr/bin/env bash

osname="$(uname -s | tr '[:upper:]' '[:lower:]')"
osarch="$(uname -m)"

mkdir -p ./scheck

curl -L -o shellcheck-stable.tar.xz "https://github.com/koalaman/shellcheck/releases/download/stable/shellcheck-stable.${osname}.${osarch}.tar.xz"

tar -xvf shellcheck-stable.tar.xz -C ./scheck

mv ./scheck/shellcheck-stable/shellcheck ./shellcheck
rm -rf ./scheck ./shellcheck-stable.tar.xz
