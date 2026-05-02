#!/usr/bin/env bash
# Fail loudly so a missing local image (e.g., docker_build.sh failed
# upstream) doesn't get masked as a successful push.
set -eo pipefail

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
  if ! docker image inspect "$registry"/"$c":"$tag" >/dev/null 2>&1; then
    echo "No local image $registry/$c:$tag — upstream build likely failed; aborting push." >&2
    exit 1
  fi
  docker push "$registry"/"$c":"$tag"
  if [ -n "$commit_sha" ]; then
    docker tag "$registry"/"$c":"$tag" "$registry"/"$c":"$commit_sha"
    docker push "$registry"/"$c":"$commit_sha"
    echo "Also pushed $registry/$c:$commit_sha"
  fi
done
