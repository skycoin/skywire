# Builder
ARG base=alpine
FROM golang:alpine  as builder

ARG BUILDINFO_LDFLAGS
ARG CGO_ENABLED=0
ENV CGO_ENABLED=${CGO_ENABLED} \
    GOOS=linux  \
    GOARCH=amd64 \
    GO111MODULE=on

COPY . skywire 

WORKDIR skywire

RUN echo $BUILDINFO_LDFLAGS

RUN go build -mod=vendor -tags netgo -ldflags="-w -s ${BUILDINFO_LDFLAGS}" \
      -o skywire-visor cmd/skywire-visor/skywire-visor.go &&\
    go build -mod=vendor -ldflags="-w -s ${BUILDINFO_LDFLAGS}" -o skywire-cli ./cmd/skywire-cli	&&\
    go build -mod=vendor -ldflags="-w -s ${BUILDINFO_LDFLAGS}" -o ./apps/skychat ./cmd/apps/skychat	&&\
	go build -mod=vendor -ldflags="-w -s ${BUILDINFO_LDFLAGS}" -o ./apps/skysocks ./cmd/apps/skysocks &&\
	go build -mod=vendor -ldflags="-w -s ${BUILDINFO_LDFLAGS}" -o ./apps/skysocks-client  ./cmd/apps/skysocks-client && \
	go build -mod=vendor -ldflags="-w -s ${BUILDINFO_LDFLAGS}" -o ./apps/vpn-server ./cmd/apps/vpn-server && \
	go build -mod=vendor -ldflags="-w -s ${BUILDINFO_LDFLAGS}" -o ./apps/vpn-client ./cmd/apps/vpn-client


## Resulting image
FROM ${base} as visor-runner

COPY --from=builder /go/skywire/skywire-visor skywire-visor
COPY --from=builder /go/skywire/apps /apps
COPY --from=builder /go/skywire/docker/images/visor/update.sh update.sh
COPY --from=builder /go/skywire/skywire-cli bin/skywire-cli
COPY --from=builder /go/skywire/docker/images/visor/entrypoint.sh entrypoint.sh

RUN ./update.sh

ENTRYPOINT [ "./entrypoint.sh" ]

# default target
FROM  visor-runner