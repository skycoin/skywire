# Cipher Package - Signature Verification

This package provides optimized signature verification for Skywire.

## Performance Optimization

The package has two implementations:

### 1. Pure Go (Default)
- **Build tag**: None (default)
- **Dependencies**: None
- **Speed**: Fast, but 3-5x slower than CGO
- **Used by**: `go run`, `go build` without tags

### 2. CGO with libsecp256k1 (Optimized)
- **Build tag**: `-tags=cgo`
- **Dependencies**: `libsecp256k1-dev`
- **Speed**: 3-5x faster signature verification
- **Used by**: Official Linux releases, optional for local builds

## Why This Matters

DMSG uses Noise protocol with frequent signature verifications during handshakes. Under high load, this can cause:
- High CPU usage
- Increased memory consumption
- Slower connection establishment

The CGO implementation using Bitcoin's secp256k1 library provides significant performance improvements.

## Building with CGO Optimization

### System Requirements

**Arch/Manjaro:**
```bash
sudo pacman -S libsecp256k1
```

**Ubuntu/Debian:**
```bash
sudo apt install libsecp256k1-dev
```

### Local Development

```bash
# Pure Go (default, no dependencies)
make build

# With CGO optimization (requires libsecp256k1-dev)
make build-merged-cgo
```

Or directly:
```bash
go build -tags=cgo ./cmd/skywire
```

### Official Releases

Linux releases are automatically built with CGO optimization using statically-linked secp256k1 libraries. This provides maximum performance while maintaining portability (no runtime dependencies).

## Implementation Details

- **cipher_nocgo.go**: Pure Go implementation (build tag: `!cgo`)
- **cipher_cgo.go**: CGO implementation (build tag: `cgo && !windows`)
- **cipher_windows.go**: Windows-specific pure Go (build tag: `windows`)

The appropriate implementation is selected at compile time based on build tags and platform.

## Testing

Both implementations are tested to ensure identical behavior:

```bash
# Test pure Go version
go test ./pkg/skywire-utilities/pkg/cipher

# Test CGO version (requires libsecp256k1-dev)
go test -tags=cgo ./pkg/skywire-utilities/pkg/cipher
```

## Troubleshooting

**Error: `secp256k1.h: No such file or directory`**

This means you're building with `-tags=cgo` but don't have libsecp256k1-dev installed. Either:
1. Install the library (see System Requirements above)
2. Build without `-tags=cgo` to use pure Go implementation
