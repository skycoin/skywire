#!/usr/bin/env bash
trap "exit" INT

## Variables
image_tag="$1"
go_buildopts="$2"
build_arch="$3"
git_branch="$(git rev-parse --abbrev-ref HEAD)"
git_commit="$(git rev-parse HEAD)"
bldkit="1"
platform="--platform=linux/amd64"

# shellcheck disable=SC2153
registry="$REGISTRY"

# shellcheck disable=SC2153
base_image=golang:1.26-alpine

if [[ "$#" != 2 ]]; then
  echo "docker_build.sh <IMAGE_TAG> <GO_BUILDOPTS>"
fi

if [[ "$go_buildopts" == "" ]]; then
  go_buildopts="-mod=vendor -ldflags\"-w -s\""
fi

if [[ "$build_arch" != "" ]]; then
  platform="--platform=$build_arch"
fi

if [[ "$git_branch" != "master" ]] && [[ "$git_branch" != "develop" ]]; then
  git_branch="develop"
fi

echo "Building using tag: $image_tag"

if [[ "$image_tag" == "e2e" ]]; then

  if [ "$DOCKER_USERNAME" != "" ] && [ "$DOCKER_PASSWORD" != "" ]; then
    echo "$DOCKER_PASSWORD" | docker login -u "$DOCKER_USERNAME" --password-stdin
  fi

  echo ============ Base images ready ======================

  if [[ "$git_branch" == "master" ]]; then
    dockerhub_image_tag="prod"
  else
    dockerhub_image_tag="test"
  fi

  echo "build dmsg discovery image"
  DOCKER_BUILDKIT="$bldkit" docker build -f docker/images/dmsg-discovery/Dockerfile \
    --build-arg build_opts="$go_buildopts" \
    --build-arg image_tag="$image_tag" \
    --build-arg base_image="skycoin/dmsg-discovery:$dockerhub_image_tag" \
    $platform \
    -t "$registry"/dmsg-discovery:"$image_tag" .

  echo "build dmsg server image"
  DOCKER_BUILDKIT="$bldkit" docker build -f docker/images/dmsg-server/Dockerfile \
    --build-arg base_image="skycoin/dmsg-server:$dockerhub_image_tag" \
    --build-arg build_opts="$go_buildopts" \
    --build-arg image_tag="$image_tag" \
    $platform \
    -t "$registry"/dmsg-server:"$image_tag" .

fi

if [[ "$image_tag" == "integration" ]]; then
  # TODO(ersonp) : the binaries build in the images need to be built with the -race flag
  if [ ! -d tmp ]; then
    mkdir -p tmp
  fi
  rm -rf ./tmp/dmsg
  cp -r ../dmsg ./tmp

  echo ============ Base images ready ======================

  echo "build dmsg discovery image"
  DOCKER_BUILDKIT="$bldkit" docker build -f docker/images/dmsg-discovery/DockerfileInt \
    $platform \
    -t "$registry"/dmsg-discovery:"$image_tag" .

  echo "build dmsg server image"
  DOCKER_BUILDKIT="$bldkit" docker build -f docker/images/dmsg-server/DockerfileInt \
    $platform \
    -t "$registry"/dmsg-server:"$image_tag" .

  rm -rf ./tmp/*
fi

echo "Build Skywire Image (tag: $image_tag)"
commit_arg=""
if [[ "$image_tag" == "prod" ]] || [[ "$image_tag" == "test" ]] || [[ "$image_tag" == "latest" ]]; then
  echo "Using remote install: github.com/skycoin/skywire@$git_commit"
  commit_arg="--build-arg commit=$git_commit"
else
  echo "Using local source build"
fi
DOCKER_BUILDKIT="$bldkit" docker build -f docker/images/skywire/Dockerfile \
  --build-arg base_image="$base_image" \
  --build-arg image_tag="$image_tag" \
  $commit_arg \
  $platform \
  -t "$registry"/skywire:"$image_tag" .

wait

echo service images built
