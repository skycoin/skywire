# Dockerfile for GeoIP CLI + API Server
# Includes the GeoLite2-City.mmdb database

# Build stage
FROM golang:1.22-alpine AS builder

WORKDIR /app

# Copy source code and database
COPY ../../cmd/geoip/geoip.go ./
COPY ../../cmd/geoip/GeoLite2-City.mmdb ./

# Install dependencies
RUN go mod init geoip-server && \
    go get github.com/oschwald/geoip2-golang/v2 && \
    go get github.com/spf13/cobra

# Build the binary
RUN go build -o geoip ./geoip.go

# Final stage
FROM alpine:latest
WORKDIR /app

# Copy binary and database
COPY --from=builder /app/geoip ./
COPY --from=builder /app/GeoLite2-City.mmdb ./

EXPOSE 8080

# Default command runs API server
CMD ["./geoip", "--api"]