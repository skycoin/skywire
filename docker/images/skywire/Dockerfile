# Builder
ARG base_image
ARG image_tag
FROM ${base_image} AS builder
ARG build_opts
ARG image_tag

ARG BUILDINFO_LDFLAGS
# CGO is ALWAYS enabled for fast crypto
ENV CGO_ENABLED=1 \
    GOOS=linux  \
    GO111MODULE=on

# Install build-base (required for CGO)
RUN apk add --no-cache build-base

# Install libsecp256k1 (3-5x faster signature verification)
RUN echo "==> Installing libsecp256k1 for fast crypto..." && \
    apk add --no-cache \
        autoconf \
        automake \
        libtool \
        git && \
    cd /tmp && \
    git clone --depth 1 https://github.com/bitcoin-core/secp256k1.git && \
    cd secp256k1 && \
    ./autogen.sh && \
    ./configure \
        --enable-module-recovery \
        --enable-module-extrakeys \
        --disable-shared \
        --with-pic && \
    make -j$(nproc) && \
    make install && \
    rm -rf /tmp/secp256k1 && \
    echo "==> libsecp256k1 installed successfully"

COPY . /skywire
WORKDIR /skywire

# Build with or without race detector
RUN if [ "$image_tag" = "integration" ]; then \
        echo "==> Building integration with race detector..." && \
        go build "${build_opts}" -race -o /release/skywire ./cmd/skywire; \
    else \
        echo "==> Building skywire with CGO..." && \
        go build "${build_opts}" -o /release/skywire ./cmd/skywire; \
    fi

## Resulting image
FROM ${base_image} AS visor-runner

# Copy libsecp256k1 static library (always present now)
COPY --from=builder /usr/local/lib/libsecp256k1.a /usr/local/lib/

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
