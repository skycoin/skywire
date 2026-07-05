# Self-contained image for the IPv6 dual-stack dmsg e2e lane (#1525).
#
# Unlike docker/images/{skywire,dmsg-server,dmsg-discovery}/Dockerfile
# (multi-stage stubs that resolve to a pre-built skywire:<tag> image
# via docker/docker_build.sh with image_tag/base_image build args),
# this Dockerfile builds `skywire` straight from the repo root — so
# `docker compose up --build` alone produces a runnable stack with no
# docker_build.sh prerequisite.
#
# One image carries both roles the compose needs:
#   skywire svc run --config ...   the deployment (dmsg-discovery +
#                                  dmsg-server in one process; that
#                                  intra-process wiring is what makes
#                                  the dmsg-first registration bootstrap
#                                  reliably — see docker/config/
#                                  services-dmsg-v6.json).
#   dmsgprobe --via tcp://...      a minimal client that completes a
#                                  real dmsg Noise handshake straight to
#                                  the server's IPv6 endpoint.
#
# Build context is the repo root (see docker-compose.e2e-v6.yml), so
# the COPY below vendors the whole module and `-mod=vendor` builds
# offline against the checked-in vendor/ tree.

ARG base_image=golang:1.26.4-alpine
FROM ${base_image} AS builder

ARG CGO_ENABLED=0
ENV CGO_ENABLED=${CGO_ENABLED} \
    GOOS=linux \
    GO111MODULE=on

COPY . /src
WORKDIR /src

# skywire carries the whole service tree (`svc run` drives the dmsg
# deployment). dmsgprobe is the standalone client used for the
# wire-level v6 proof: `dmsgprobe --via tcp://<pk>@[v6]:port` performs a
# full dmsg Noise handshake straight to a TCP address (no discovery), so
# a completed handshake to the server's IPv6 endpoint proves the dmsg
# protocol works over IPv6. netgo keeps DNS pure-Go so the alpine
# runtime has no libc-resolver quirks to trip over on the dual-stack
# bridge.
RUN go build -tags netgo -mod=vendor -ldflags="-w -s" -o /release/skywire . && \
    go build -tags netgo -mod=vendor -ldflags="-w -s" -o /release/dmsgprobe ./cmd/dmsg/dmsgprobe

## Runtime image
FROM alpine:latest

COPY --from=builder /release/skywire   /usr/local/bin/skywire
COPY --from=builder /release/dmsgprobe /usr/local/bin/dmsgprobe

# ca-certificates+curl: HTTP probes of the discovery registry.
# iproute2 (ss) + netcat-openbsd (nc -6): the wire-level v6 assertions
# run from inside the client container.
RUN apk add --no-cache ca-certificates curl bash iproute2 netcat-openbsd

STOPSIGNAL SIGINT

# No ENTRYPOINT: each compose service supplies its own full command.
