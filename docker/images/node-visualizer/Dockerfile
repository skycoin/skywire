ARG image_tag
ARG base_image

FROM ${base_image} AS builder
ARG build_opts

COPY . /skywire-services
WORKDIR /skywire-services

RUN go build "${build_opts}" -o /release/node-visualizer ./cmd/node-visualizer

FROM alpine AS prod
COPY --from=builder /release/node-visualizer /release/node-visualizer
ENTRYPOINT ["/release/node-visualizer"]

FROM prod AS test

# OS image
FROM alpine AS e2e
COPY ./docker/common/install-prequisites.sh /release/install-prequisites.sh
RUN sh -c /release/install-prequisites.sh cert-only \
    && rm -rf /release/install-prequisites.sh

COPY --from=builder /release/node-visualizer /release/node-visualizer
ENTRYPOINT ["/release/node-visualizer"]

FROM e2e AS integration

FROM ${image_tag}
