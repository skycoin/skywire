#!/usr/bin/env bash

tag="$1"

# shellcheck disable=SC2153
registry="$REGISTRY"

if [ -z "$registry" ]; then
	registry="skycoin"
fi

if [ -z "$tag" ]; then
  echo "Image tag is not provided. Usage: sh ./docker/docker_push.sh <image_tag>"
  exit
fi

declare -a images_arr=(
  "transport-discovery"
  "route-finder"
  "setup-node"
  "address-resolver"
  "uptime-tracker"
  "network-monitor"
  "node-visualizer"
  "config-bootstrapper"
  "transport-setup"
)

echo "Pushing to $registry using tag: $tag"

for c in "${images_arr[@]}"; do
  docker push "$registry"/"$c":"$tag"
done
