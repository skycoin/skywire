FROM golang:1.26-alpine AS builder
ARG CGO_ENABLED=0

ENV CGO_ENABLED=${CGO_ENABLED} \
	GOOS=linux  \
	GOARCH=amd64 \
	GO111MODULE=on

COPY . /dmsg
WORKDIR /dmsg

# Build dmsg binary from repo root
RUN go build -tags netgo -mod=vendor "-ldflags=-w -s" -o /release/dmsg .

# Build image
FROM alpine:latest

COPY --from=builder /release/dmsg /usr/local/bin/dmsg

EXPOSE 9090
STOPSIGNAL SIGINT
ENTRYPOINT [ "dmsg", "disc" ]
