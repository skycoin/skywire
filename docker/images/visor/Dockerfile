# Builder
ARG base=alpine
FROM golang:alpine  as builder

ARG BUILDINFO_LDFLAGS
ARG CGO_ENABLED=0
ENV CGO_ENABLED=${CGO_ENABLED} \
    GOOS=linux  \
    GOARCH=amd64 \
    GO111MODULE=on

COPY . /skywire

WORKDIR /skywire

RUN apk add --no-cache make && \
    sh -c /skywire/docker/images/visor/install-preq.sh && \
    make host-apps && \
    make build-deploy && \
    cp ./apps/vpn-server /release/apps/ && \
    cp ./apps/vpn-client /release/apps/ && \
		cp ./apps/skysocks /release/apps/ && \
		cp ./apps/skychat /release/apps/ 

## Resulting image
FROM ${base} as visor-runner

COPY --from=builder /skywire/docker/images/visor/install-preq.sh /release/install-preq.sh
COPY --from=builder /skywire/docker/images/visor/entrypoint.sh /entrypoint.sh
COPY --from=builder /release /release

RUN sh -c /release/install-preq.sh \
    && rm -rf /release/install-preq.sh \
    && mkdir -p /opt/skywire

STOPSIGNAL SIGINT

ENTRYPOINT [ "/entrypoint.sh" ]

# default target
FROM  visor-runner
