#!/bin/sh

if type apt >/dev/null; then
  apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates iptables &&
    rm -rf /var/lib/apt/lists/*
fi

if [ "$1" = "cert-only" ]; then
  if type apk >/dev/null; then
    apk update
    apk add --no-cache ca-certificates bash iptables
    update-ca-certificates --fresh
  fi
else
  if type apk >/dev/null; then
    apk update
    apk add --no-cache ca-certificates openssl iproute2 git bash iptables
    update-ca-certificates --fresh
  fi
fi
