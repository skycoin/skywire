ARG base_image
ARG image_tag

FROM ${base_image} AS builder

ARG build_opts

COPY . /skywire-services
WORKDIR /skywire-services

RUN go build "${build_opts}" -o /release/transport-discovery \
    ./cmd/transport-discovery


FROM alpine AS prod
COPY --from=builder /release/transport-discovery /release/transport-discovery
ENTRYPOINT ["/release/transport-discovery"]

FROM prod AS test

# OS image
FROM alpine AS e2e

COPY ./docker/common/install-prequisites.sh /release/install-prequisites.sh
RUN sh -c /release/install-prequisites.sh cert-only \
    && rm -rf /release/install-prequisites.sh

COPY --from=builder /release/transport-discovery /release/transport-discovery
ENTRYPOINT ["/release/transport-discovery"]

FROM e2e AS integration

FROM ${image_tag}
