# Builder
ARG base_image
ARG image_tag
FROM ${base_image} AS builder
ARG image_tag

ARG CGO_ENABLED=0
ENV CGO_ENABLED=${CGO_ENABLED} \
    GOOS=linux  \
    GO111MODULE=on \
    GOBIN=/release

COPY . /skywire
WORKDIR /skywire

# Install git for VCS version stamping (go install needs git to read .git directory)
RUN apk add --no-cache git

# Build using go install to get proper version info from runtime/debug.ReadBuildInfo()
# The -w -s flags strip debug info to reduce binary size
RUN if [ "$image_tag" = "integration" ]; then \
        apk add --no-cache build-base && \
        CGO_ENABLED=1 go install -ldflags="-w -s" -race .; \
    else \
        go install -ldflags="-w -s" .; \
    fi

## Resulting image
FROM ${base_image} AS visor-runner

COPY --from=builder /skywire/docker/common/install-prequisites.sh /release/install-prequisites.sh
COPY --from=builder /release /release

RUN sh -c /release/install-prequisites.sh \
    && rm -rf /release/install-prequisites.sh \
    && mkdir -p /opt/skywire \
    && apk add curl

STOPSIGNAL SIGINT

ENTRYPOINT [ "/release/skywire" ]

# default target
FROM visor-runner AS prod

FROM visor-runner AS test

FROM visor-runner AS latest

FROM visor-runner AS e2e

FROM visor-runner AS integration

FROM ${image_tag}
