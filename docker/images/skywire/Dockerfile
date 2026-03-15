# Builder
ARG base_image
ARG image_tag
FROM ${base_image} AS builder
ARG image_tag
ARG commit

ARG CGO_ENABLED=0
ENV CGO_ENABLED=${CGO_ENABLED} \
    GOOS=linux  \
    GO111MODULE=on \
    GOBIN=/release \
    GONOSUMDB=github.com/skycoin/skywire \
    GONOSUMCHECK=github.com/skycoin/skywire

COPY . /src

RUN apk add --no-cache git && \
    if [ -n "$commit" ]; then \
        if [ "$image_tag" = "integration" ]; then \
            apk add --no-cache build-base && \
            CGO_ENABLED=1 GOPROXY=direct go install -ldflags="-w -s" -race github.com/skycoin/skywire@${commit}; \
        else \
            GOPROXY=direct go install -ldflags="-w -s" github.com/skycoin/skywire@${commit}; \
        fi; \
    else \
        cd /src && \
        if [ "$image_tag" = "integration" ]; then \
            apk add --no-cache build-base && \
            CGO_ENABLED=1 go install -ldflags="-w -s" -race .; \
        else \
            go install -ldflags="-w -s" .; \
        fi; \
    fi

## Resulting image
FROM ${base_image} AS visor-runner

COPY docker/common/install-prequisites.sh /tmp/install-prequisites.sh
COPY --from=builder /release /release

RUN sh -c /tmp/install-prequisites.sh \
    && rm -rf /tmp/install-prequisites.sh \
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
