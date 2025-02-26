ARG image_tag
ARG base_image

FROM ${base_image} AS builder

ARG build_opts

COPY . /skywire-services
WORKDIR /skywire-services

RUN go build "${build_opts}" -o /release/network-monitor ./cmd/network-monitor && \
    go build "${build_opts}" -o /release/vpn-client ./cmd/vpn-lite-client

FROM alpine AS prod
WORKDIR /release
COPY --from=builder /release/vpn-client /apps/vpn-client
COPY --from=builder /release/network-monitor /release/network-monitor
ENTRYPOINT ["/release/network-monitor"]

FROM prod AS test

# OS image
FROM alpine AS e2e
WORKDIR /release

COPY ./docker/common/install-prequisites.sh /release/install-prequisites.sh
RUN sh -c /release/install-prequisites.sh cert-only \
    && rm -rf /release/install-prequisites.sh

COPY --from=builder /release/vpn-client /release/vpn-client
COPY --from=builder /release/network-monitor /release/network-monitor
ENTRYPOINT ["/release/network-monitor"]

FROM e2e AS integration

FROM ${image_tag}
