#!/usr/bin/env bash
trap "exit" INT

## Variables
image_tag="$1"
go_buildopts="$2"
build_arch="$3"
git_branch="$(git rev-parse --abbrev-ref HEAD)"
bldkit="1"
platform="--platform=linux/amd64"

# shellcheck disable=SC2153
registry="$REGISTRY"

# shellcheck disable=SC2153
base_image=golang:1.24-alpine

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

echo "Build skywire visor image"
DOCKER_BUILDKIT="$bldkit" docker build -f docker/images/skywire-visor/Dockerfile \
  --build-arg base_image="$base_image" \
  --build-arg build_opts="$go_buildopts" \
  --build-arg image_tag="$image_tag" \
  $platform \
  -t "$registry"/skywire-visor:"$image_tag" .


if [[ "$image_tag" == "e2e" ]]; then

  if [ "$DOCKER_USERNAME" != "" ] && [ "$DOCKER_PASSWORD" != "" ]; then
    echo "$DOCKER_PASSWORD" | docker login -u "$DOCKER_USERNAME" --password-stdin
  fi

  # TODO(ersonp): instead of cloning the git branch we should directly use the docker image od SD from dockerhub like we doing for dmsg 
  git clone https://github.com/skycoin/skycoin-service-discovery.git --depth 1 --branch "$git_branch" ./tmp/skycoin-service-discovery

  if [ ! -d ./tmp/skycoin-service-discovery ]; then
    echo "failed to clone skycoin-service-discovery" &&
      exit 1
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

  echo "build service discovery image"
  DOCKER_BUILDKIT="$bldkit" docker build -f docker/images/service-discovery/Dockerfile \
    --build-arg base_image="$base_image" \
    --build-arg build_opts="$go_buildopts" \
    --build-arg image_tag="$image_tag" \
    $platform \
    -t "$registry"/service-discovery:"$image_tag" .

  rm -rf ./tmp/skycoin-service-discovery
fi

if [[ "$image_tag" == "integration" ]]; then
  # TODO(ersonp) : the binaries build in the images need to be built with the -race flag
  rm -rf ./tmp/skycoin-service-discovery
  rm -rf ./tmp/dmsg
  cp -r ../skycoin-service-discovery ./tmp
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

  echo "build service discovery image"
  DOCKER_BUILDKIT="$bldkit" docker build -f docker/images/service-discovery/Dockerfile \
    --build-arg base_image="$base_image" \
    --build-arg build_opts="$go_buildopts" \
    --build-arg image_tag="$image_tag" \
    $platform \
    -t "$registry"/service-discovery:"$image_tag" .

  rm -rf ./tmp/*
fi

echo "Build skywire visor image"
DOCKER_BUILDKIT="$bldkit" docker build -f docker/images/skywire-visor/Dockerfile \
  --build-arg base_image="$base_image" \
  --build-arg build_opts="$go_buildopts" \
  --build-arg image_tag="$image_tag" \
  $platform \
  -t "$registry"/skywire-visor:"$image_tag" .

echo "Build transport discovery image"
DOCKER_BUILDKIT="$bldkit" docker build -f docker/images/transport-discovery/Dockerfile \
  --build-arg base_image="$base_image" \
  --build-arg build_opts="$go_buildopts" \
  --build-arg image_tag="$image_tag" \
  $platform \
  -t "$registry"/transport-discovery:"$image_tag" .

echo "build route finder image"
DOCKER_BUILDKIT="$bldkit" docker build -f docker/images/route-finder/Dockerfile \
  --build-arg base_image="$base_image" \
  --build-arg build_opts="$go_buildopts" \
  --build-arg image_tag="$image_tag" \
  $platform \
  -t "$registry"/route-finder:"$image_tag" .

echo "build setup node image"
DOCKER_BUILDKIT="$bldkit" docker build -f docker/images/setup-node/Dockerfile \
  --build-arg base_image="$base_image" \
  --build-arg build_opts="$go_buildopts" \
  --build-arg image_tag="$image_tag" \
  $platform \
  -t "$registry"/setup-node:"$image_tag" .

echo "build address resolver image"
DOCKER_BUILDKIT="$bldkit" docker build -f docker/images/address-resolver/Dockerfile \
  --build-arg base_image="$base_image" \
  --build-arg build_opts="$go_buildopts" \
  --build-arg image_tag="$image_tag" \
  $platform \
  -t "$registry"/address-resolver:"$image_tag" .

echo "build uptime tracker image"
DOCKER_BUILDKIT="$bldkit" docker build -f docker/images/uptime-tracker/Dockerfile \
  --build-arg build_opts="$go_buildopts" \
  --build-arg image_tag="$image_tag" \
  --build-arg base_image="$base_image" \
  $platform \
  -t "$registry"/uptime-tracker:"$image_tag" .

echo "build node visualizer image"
DOCKER_BUILDKIT="$bldkit" docker build -f docker/images/node-visualizer/Dockerfile \
  --build-arg base_image="$base_image" \
  --build-arg build_opts="$go_buildopts" \
  --build-arg image_tag="$image_tag" \
  $platform \
  -t "$registry"/node-visualizer:"$image_tag" .

echo "building network monitor image"
DOCKER_BUILDKIT="$bldkit" docker build -f docker/images/network-monitor/Dockerfile \
  --build-arg base_image="$base_image" \
  --build-arg build_opts="$go_buildopts" \
  --build-arg image_tag="$image_tag" \
  $platform \
  -t "$registry"/network-monitor:"$image_tag" .

echo "building config bootstrapper image"
DOCKER_BUILDKIT="$bldkit" docker build -f docker/images/config-bootstrapper/Dockerfile \
  --build-arg base_image="$base_image" \
  --build-arg build_opts="$go_buildopts" \
  --build-arg image_tag="$image_tag" \
  $platform \
  -t "$registry"/config-bootstrapper:"$image_tag"

echo "building transport setup image"
DOCKER_BUILDKIT="$bldkit" docker build -f docker/images/transport-setup/Dockerfile \
  --build-arg base_image="$base_image" \
  --build-arg build_opts="$go_buildopts" \
  --build-arg image_tag="$image_tag" \
  $platform \
  -t "$registry"/transport-setup:"$image_tag" .

wait

echo service images built
