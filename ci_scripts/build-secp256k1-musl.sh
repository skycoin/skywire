#!/bin/bash
set -e

# Build secp256k1 static library for a musl target
# Usage: ./build-secp256k1-musl.sh <arch> <toolchain-prefix> <sysroot>
# Example: ./build-secp256k1-musl.sh amd64 x86_64-linux-musl ./musl-data/x86_64-linux-musl-cross

ARCH=$1
TOOLCHAIN_PREFIX=$2
SYSROOT=$3

if [ -z "$ARCH" ] || [ -z "$TOOLCHAIN_PREFIX" ] || [ -z "$SYSROOT" ]; then
    echo "Usage: $0 <arch> <toolchain-prefix> <sysroot>"
    echo "Example: $0 amd64 x86_64-linux-musl ./musl-data/x86_64-linux-musl-cross"
    exit 1
fi

SECP256K1_VERSION="v0.6.0"
BUILD_DIR="/tmp/secp256k1-build-${ARCH}"

echo "Building secp256k1 ${SECP256K1_VERSION} for ${ARCH} (${TOOLCHAIN_PREFIX})"

# Clean previous build
rm -rf "${BUILD_DIR}"
mkdir -p "${BUILD_DIR}"
cd "${BUILD_DIR}"

# Download secp256k1
wget -q "https://github.com/bitcoin-core/secp256k1/archive/refs/tags/${SECP256K1_VERSION}.tar.gz"
tar -xzf "${SECP256K1_VERSION}.tar.gz"
cd "secp256k1-${SECP256K1_VERSION#v}"

# Configure and build
./autogen.sh
./configure \
    --host="${TOOLCHAIN_PREFIX}" \
    CC="${SYSROOT}/bin/${TOOLCHAIN_PREFIX}-gcc" \
    --prefix="${SYSROOT}/${TOOLCHAIN_PREFIX}" \
    --enable-static \
    --disable-shared \
    --enable-module-recovery \
    --disable-benchmark \
    --disable-tests \
    --disable-exhaustive-tests

make -j$(nproc)

# Install to sysroot
make install

echo "✓ Installed libsecp256k1.a to ${SYSROOT}/${TOOLCHAIN_PREFIX}/lib/"
ls -lh "${SYSROOT}/${TOOLCHAIN_PREFIX}/lib/libsecp256k1.a"
