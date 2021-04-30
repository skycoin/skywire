#!/usr/bin/env bash

function print_usage() {
  echo "Use: $0 [-t <docker_image_tag_name>] [-p | -b]"
  echo "use -p for push (it builds and push the image)"
  echo "use -b for build image locally"
}

if [[ $# -ne 3 ]]; then
  print_usage
  exit 0
fi

function docker_build() {
  docker image build \
    --tag=skycoin/skywire:"$tag" \
    -f ./docker/images/visor/Dockerfile .
}

function docker_push() {
  echo "pushed"
  #  docker login -u "$DOCKER_USERNAME" -p "$DOCKER_PASSWORD"
  #  docker tag skycoin/skywire:"$tag" skycoin/skywire:"$tag"
  #  docker image push skycoin/skywire:"$tag"
}

while getopts ":t:pb" o; do
  case "${o}" in
  t)
    tag="$(echo "${OPTARG}" | tr -d '[:space:]')"
    if [[ $tag == "develop" ]]; then
      tag="test"
    fi
    ;;
  p)
    docker_build
    docker_push
    ;;
  b)
    docker_build
    ;;
  *)
    print_usage
    ;;
  esac
done
