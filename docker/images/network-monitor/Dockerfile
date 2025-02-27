ARG image_tag
ARG base_image

FROM ${base_image} AS builder
ARG build_opts
ARG image_tag

COPY . /skywire-services
WORKDIR /skywire-services

RUN if [ "$image_tag" = "integration" ]; then \
        apk add --no-cache build-base && \
        CGO_ENABLED=1 go build "${build_opts}" -race -o /release/network-monitor ./cmd/network-monitor; \
    else \
        go build "${build_opts}" -o /release/network-monitor ./cmd/network-monitor; \
    fi


FROM alpine AS prod
WORKDIR /release
COPY --from=builder /release/network-monitor /release/network-monitor
ENTRYPOINT ["/release/network-monitor"]

FROM prod AS test

# OS image
FROM alpine AS e2e
WORKDIR /release

COPY ./docker/common/install-prequisites.sh /release/install-prequisites.sh
RUN sh -c /release/install-prequisites.sh cert-only \
    && rm -rf /release/install-prequisites.sh

COPY --from=builder /release/network-monitor /release/network-monitor
ENTRYPOINT ["/release/network-monitor"]

FROM e2e AS integration

FROM ${image_tag}
