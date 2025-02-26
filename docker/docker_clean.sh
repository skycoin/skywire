#!/usr/bin/env bash

image_tag="$1"

if [ -z "$image_tag" ]; then
	image_tag=e2e
fi

declare -a images_arr=(
  "skycoin/setup-node:${image_tag}"
  "skycoin/route-finder:${image_tag}"
  "skycoin/transport-discovery:${image_tag}"
  "skycoin/address-resolver:${image_tag}"
  "skycoin/dmsg-server:${image_tag}"
  "skycoin/dmsg-discovery:${image_tag}"
  "skycoin/skywire-visor:${image_tag}"
  "skycoin/service-discovery:${image_tag}"
  "skycoin/uptime-tracker:${image_tag}"
  "skycoin/network-monitor:${image_tag}"
  "skycoin/node-visualizer:${image_tag}"
  "skycoin/config-bootstrapper:${image_tag}"
  "skycoin/transport-setup:${image_tag}"
)

for i in "${images_arr[@]}"; do
  docker rmi -f "$i"
done
