# Builder
ARG base_image
ARG image_tag
FROM golang:1.24-alpine AS builder

ARG BUILDINFO_LDFLAGS
ARG CGO_ENABLED=0
ENV CGO_ENABLED=${CGO_ENABLED} \
    GOOS=linux  \
    GO111MODULE=on

COPY . /skywire

WORKDIR /skywire

RUN apk add --no-cache make git && \
    sh -c /skywire/docker/common/install-prequisites.sh && \
    make build-deploy

## Resulting image
FROM ${base_image} AS visor-runner

COPY --from=builder /skywire/docker/common/install-prequisites.sh /release/install-prequisites.sh
COPY --from=builder /release /release

RUN sh -c /release/install-prequisites.sh \
    && rm -rf /release/install-prequisites.sh \
    && mkdir -p /opt/skywire

STOPSIGNAL SIGINT

ENTRYPOINT [ "/release/skywire" ]

# default target
FROM visor-runner AS prod

FROM visor-runner AS test

FROM visor-runner AS latest

FROM visor-runner AS e2e

FROM visor-runner AS integration

FROM ${image_tag}
