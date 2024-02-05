# Builder
ARG base=alpine
FROM golang:alpine3.13 as builder

ARG BUILDINFO_LDFLAGS
ARG CGO_ENABLED=0
ENV CGO_ENABLED=${CGO_ENABLED} \
    GOOS=linux  \
    GO111MODULE=on

COPY . /skywire

WORKDIR /skywire

RUN apk add --no-cache make git && \
    sh -c /skywire/docker/images/visor/install-preq.sh && \
    make build-deploy && \
    mkdir -p /apps && \
    cp ./build/apps/vpn-server /apps/ && \
    cp ./build/apps/vpn-client /apps/ && \
	cp ./build/apps/skysocks /apps/ && \
	cp ./build/apps/skysocks-client /apps/ && \
	cp ./build/apps/skychat /apps/ \
    cp ./build/apps/skyhttp /apps/

## Resulting image
FROM ${base} as visor-runner

COPY --from=builder /skywire/docker/images/visor/install-preq.sh /release/install-preq.sh
COPY --from=builder /skywire/docker/images/visor/entrypoint.sh /entrypoint.sh
COPY --from=builder /release /release
COPY --from=builder /apps /apps

RUN sh -c /release/install-preq.sh \
    && rm -rf /release/install-preq.sh \
    && mkdir -p /opt/skywire

STOPSIGNAL SIGINT

ENTRYPOINT [ "/entrypoint.sh" ]

# default target
FROM  visor-runner
