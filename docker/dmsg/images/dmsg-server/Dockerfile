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

RUN mkdir -p /e2e && \
    echo '{\
  "public_key": "039346fcae983d7e91d923f5331b725b6892baa27bf779e563014660aa05e273e9",\
  "secret_key": "83549a8bd2b9399bf01d60618904e45b82e5400589fbe8b48377dd7d887e5823",\
  "discovery": "http://172.20.0.3:9090",\
  "public_address": "172.20.0.4:8080",\
  "local_address": ":8080",\
  "health_endpoint_address": ":8081",\
  "log_level": "info",\
  "max_sessions": 2048\
}' > /e2e/dmsg-server.json

STOPSIGNAL SIGINT

ENTRYPOINT [ "dmsg", "server" ]
