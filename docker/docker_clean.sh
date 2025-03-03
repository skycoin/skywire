#!/usr/bin/env bash

image_tag="$1"

if [ -z "$image_tag" ]; then
	image_tag=e2e
fi

declare -a images_arr=(
  "skycoin/dmsg-server:${image_tag}"
  "skycoin/dmsg-discovery:${image_tag}"
  "skycoin/skywire-visor:${image_tag}"
  "skycoin/service-discovery:${image_tag}"
)

for i in "${images_arr[@]}"; do
  docker rmi -f "$i"
done
