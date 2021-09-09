#!/usr/bin/env bash

osname="$(uname -s)"
arch="$(uname -m)"
gh_version="2.0.0"
tag="$1"
golang_cross_version="$2"

git checkout "${tag}"

if [[ ${GITHUB_TOKEN} == "" ]]; then
  echo "GITHUB_TOKEN environment variable has to be set"
  exit 1
fi

if ! command -v gh &>/dev/null; then
  echo "Downloading gh binary"
  case ${osname} in
  Darwin)
    osname="macOS"
    ;;
  Linux)
    osname="linux"
    ;;
  *)
    echo "error detecting OS, not supported"
    exit 1
    ;;
  esac

  case ${arch} in
  x86_64)
    arch="amd64"
    ;;
  aarch64)
    arch="arm64"
    ;;
  *)
    arch=${arch}
    ;;
  esac

  curl -sSL -o /tmp/gh.tar.gz "https://github.com/cli/cli/releases/download/v${gh_version}/gh_${gh_version}_${osname}_${arch}.tar.gz"
  tar xf /tmp/gh.tar.gz
  sudo mv gh_${gh_version}_${osname}_${arch}/bin/gh /usr/local/bin/gh
fi

function push() {
  goreleaser --rm-dist
  cp -R ./dist ./dist-non-systray
  docker run --rm --privileged \
    -v "$(pwd)":/go/src/github.com/skycoin/skywire \
    -v /var/run/docker.sock:/var/run/docker.sock \
    -v "${GOPATH}"/src:/go/src \
    -v "$(pwd)"/sysroot:/sysroot \
    -e GITHUB_TOKEN="${GITHUB_TOKEN}" \
    skycoin/golang-cross:"${golang_cross_version}" -f /go/src/github.com/skycoin/skywire/.goreleaser-systray.yml --rm-dist

  cat ./dist/checksum.txt >>./dist-non-systray/checksum.txt

  for archive in ./dist-non/systray/*.tar.gz; do
    gh release upload "${tag}" "${archive}"
  done

  gh release upload --clobber "${tag}" ./dist-non-systray/checksum.txt
}

push
