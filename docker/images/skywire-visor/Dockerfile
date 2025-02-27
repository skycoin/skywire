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

WORKDIR /

RUN sh -c /skywire/docker/common/install-prequisites.sh && \
    if [ "$image_tag" = "integration" ]; then \
        apk add --no-cache make bash build-base && \
        make build-race; \
    else \
        apk add --no-cache make bash && \
        make build-deploy; \
    fi

## Resulting image
FROM ${base_image} AS visor-runner

COPY --from=builder /skywire/docker/common/install-prequisites.sh /release/install-prequisites.sh
COPY --from=builder /release /release

RUN sh -c /release/install-prequisites.sh \
    && rm -rf /release/install-prequisites.sh \
    && mkdir -p /opt/skywire && \
    apk add curl 

STOPSIGNAL SIGINT

ENTRYPOINT [ "/release/skywire" ]

# default target
FROM visor-runner AS prod

FROM visor-runner AS test

FROM visor-runner AS latest

FROM visor-runner AS e2e

FROM visor-runner AS integration

FROM ${image_tag}
