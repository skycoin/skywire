# Builder
ARG base_image
ARG image_tag
FROM ${base_image} AS builder
ARG build_opts
ARG image_tag

ARG BUILDINFO_LDFLAGS
ARG CGO_ENABLED=0
ENV CGO_ENABLED=${CGO_ENABLED} \
    GOOS=linux  \
    GO111MODULE=on

COPY . /skywire-services
WORKDIR /skywire-services

RUN if [ "$image_tag" = "integration" ]; then \
        apk add --no-cache build-base && \
        CGO_ENABLED=1 go build "${build_opts}" -race -o /release/skywire-services ./cmd/skywire-services; \
    else \
        go build "${build_opts}" -o /release/skywire-services ./cmd/skywire-services; \
    fi

## Resulting image
FROM ${base_image} AS visor-runner

COPY --from=builder /skywire-services/docker/common/install-prequisites.sh /release/install-prequisites.sh
COPY --from=builder /release /release

RUN sh -c /release/install-prequisites.sh \
    && rm -rf /release/install-prequisites.sh \
    && apk add curl

STOPSIGNAL SIGINT

ENTRYPOINT [ "/release/skywire-services" ]

# default target
FROM visor-runner AS prod

FROM visor-runner AS test

FROM visor-runner AS latest

FROM visor-runner AS e2e

FROM visor-runner AS integration

FROM ${image_tag}
