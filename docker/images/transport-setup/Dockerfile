ARG image_tag
ARG base_image

FROM ${base_image} AS builder

ARG build_opts

COPY . /skywire-services
WORKDIR /skywire-services

RUN go build "${build_opts}" -o /release/transport-setup ./cmd/transport-setup && \
    mv ./docker/config/transport-setup.json /release/transport-setup.json

FROM alpine AS prod

WORKDIR /release
COPY --from=builder /release/transport-setup /release/transport-setup
COPY --from=builder /release/transport-setup.json /release/transport-setup.json
ENTRYPOINT ["/release/transport-setup"]

FROM prod AS test

# OS image
FROM alpine AS e2e
WORKDIR /release

COPY ./docker/common/install-prequisites.sh /release/install-prequisites.sh
RUN sh -c /release/install-prequisites.sh cert-only \
    && rm -rf /release/install-prequisites.sh

COPY --from=builder /release/transport-setup /release/transport-setup
COPY --from=builder /release/transport-setup.json /release/transport-setup.json
ENTRYPOINT ["/release/transport-setup"]

FROM e2e AS integration

FROM ${image_tag}
