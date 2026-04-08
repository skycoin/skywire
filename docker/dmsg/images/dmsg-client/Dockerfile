# Builder
ARG base_image=golang:1.26-alpine
FROM ${base_image} AS builder

ARG CGO_ENABLED=0
ENV CGO_ENABLED=${CGO_ENABLED} \
    GOOS=linux  \
    GO111MODULE=on

COPY . /dmsg
WORKDIR /dmsg

RUN go build -o /release/dmsg . && \
    go build -o /release/testserver ./internal/e2e/testserver

## Resulting image
FROM alpine:latest

COPY --from=builder /release/dmsg /usr/local/bin/dmsg
COPY --from=builder /release/testserver /usr/local/bin/testserver

RUN apk add --no-cache ca-certificates curl python3 bash net-tools

ENTRYPOINT ["/usr/local/bin/dmsg"]
