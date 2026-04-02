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
  "skywire"
)

echo "Pushing to $registry using tag: $tag"

# Also tag with the git commit SHA so specific versions can be pulled
commit_sha="$(git rev-parse --short HEAD 2>/dev/null || echo "")"

for c in "${images_arr[@]}"; do
  docker push "$registry"/"$c":"$tag"
  if [ -n "$commit_sha" ]; then
    docker tag "$registry"/"$c":"$tag" "$registry"/"$c":"$commit_sha"
    docker push "$registry"/"$c":"$commit_sha"
    echo "Also pushed $registry/$c:$commit_sha"
  fi
done
