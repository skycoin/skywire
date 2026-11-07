# gopus

gopus encodes and decodes Opus audio in pure Go, without cgo or an external
codec library. It supports voice and music, mono and stereo, multistream and
ambisonics, Ogg files, and RTP RED recovery.

The caller-buffer packet APIs perform zero-allocation processing after warmup.
Ordinary builds use scalar Go; Go 1.27's experimental SIMD support adds
CPU-specific kernels. Correctness is checked against pinned libopus 1.6.1 and
the RFC 8251 test vectors; see [parity and testing](#parity--testing) for the
exact guarantees and tested configurations.

[API reference](https://pkg.go.dev/github.com/thesyncim/gopus) ·
[Quick start](#quick-start) · [Working with audio](#working-with-audio) ·
[Examples](#examples) · [Build options](#build-options) ·
[Performance](#performance) · [Parity & testing](#parity--testing) ·
[Contributing](CONTRIBUTING.md)

## Install

gopus v0.2.3 requires Go 1.27.3 or newer.

```sh
go get github.com/thesyncim/gopus@v0.2.3
```

See the [v0.2.3 documentation](https://github.com/thesyncim/gopus/tree/v0.2.3)
for this release.

## Quick start

Encode and decode a 20 ms stereo frame at 48 kHz. This complete program uses
silence; replace `pcm` with your interleaved audio samples:

```go
package main

import (
	"fmt"
	"log"

	"github.com/thesyncim/gopus"
)

func main() {
	const (
		sampleRate = 48000
		channels   = 2
		frameSize  = 960 // samples per channel
	)

	enc, err := gopus.NewEncoder(gopus.EncoderConfig{
		SampleRate:  sampleRate,
		Channels:    channels,
		Application: gopus.ApplicationAudio,
	})
	if err != nil {
		log.Fatal(err)
	}
	cfg := gopus.DefaultDecoderConfig(sampleRate, channels)
	dec, err := gopus.NewDecoder(cfg)
	if err != nil {
		log.Fatal(err)
	}

	pcm := make([]float32, frameSize*channels) // interleaved input, normally [-1, 1]
	packet := make([]byte, cfg.MaxPacketBytes)
	out := make([]float32, cfg.MaxPacketSamples*channels)

	n, err := enc.Encode(pcm, packet) // bytes written
	if err != nil {
		log.Fatal(err)
	}
	samples, err := dec.Decode(packet[:n], out) // samples per channel
	if err != nil {
		log.Fatal(err)
	}
	decoded := out[:samples*channels]
	fmt.Printf("decoded %d samples per channel (%d total)\n", samples, len(decoded))
}
```

## Working with audio

### PCM and reusable buffers

Use one encoder and decoder per stream, and allocate PCM and packet buffers
outside the audio loop. `Encode`, `EncodeInt16`, `EncodeInt24`, `Decode`,
`DecodeInt16` and `DecodeInt24` use caller-owned output. `EncodeFloat32`,
`EncodeInt16Slice`, `EncodeInt24Slice` and `DecodeInt24Slice` return newly
allocated slices; use the caller-buffer methods when zero allocations are required.

With `enc`, `dec`, and `cfg` from the quick start, the same round trip works
with `int16` PCM:

```go
// Allocate once, outside the audio loop. FrameSize counts samples per channel.
input := make([]int16, enc.FrameSize()*channels) // 960 × 2 for 20 ms stereo
packetBuf := make([]byte, cfg.MaxPacketBytes)    // length sets the encode byte budget
output := make([]int16, cfg.MaxPacketSamples*channels)

// Stereo is interleaved: L0, R0, L1, R1, ... . Fill the rest from your audio source.
input[0], input[1] = 1200, -1200

bytesWritten, err := enc.EncodeInt16(input, packetBuf)
if err != nil {
	log.Fatal(err)
}
samples, err := dec.DecodeInt16(packetBuf[:bytesWritten], output)
if err != nil {
	log.Fatal(err)
}

// Decode returns samples per channel, so include both channels in the slice.
decoded := output[:samples*channels]
fmt.Printf("decoded %d interleaved values\n", len(decoded))
// Consume decoded before the next decode overwrites output.
```

The encoder starts at 20 ms per frame. `SetFrameSize` changes the input length
in samples per channel: for 10 ms at 48 kHz, use `enc.SetFrameSize(480)` and
supply exactly `480*channels` PCM values. `FrameSize()` reports that required
length per channel. Decode reports the packet's actual sample count per channel,
independently of any encoder instance.

The packet buffer's **length**, not capacity, sets the encode byte budget.
Pass `packetBuf` on each call and send only `packetBuf[:bytesWritten]`; keeping
the shortened slice as the next call's buffer also shrinks its budget. Likewise,
pass the full reusable PCM output slice to decode coded packets and consume only
`output[:samples*channels]`. Consume or copy output before reusing its buffer.

`Encode`/`Decode` use `float32` PCM, normally in [-1, 1]. The `Int24` methods
use right-justified signed 24-bit PCM in `[]int32`; decode output is not clamped
to 24 bits, so gain can produce values outside [-8,388,608, 8,388,607]. All formats
use the same interleaved layout.

The default decoder limits are 5,760 samples per channel and 1,500 packet bytes.
Configure larger limits before constructing the decoder when needed; for example,
120 ms at native 96 kHz needs 11,520 samples per channel.

Codec instances retain stream history and are not safe for concurrent calls.
`Reset` starts a new stream with the same format and ordinary controls; encoder
reset also disables DRED emission. Construction and initial warmup can allocate.

### Packet loss

Choose **one** recovery path for each missing frame. Your application decides
when a packet is lost and how long it can wait for the next one.

**Conceal a loss immediately.** Pass `nil` and size the output to the missing
duration. Using the 48 kHz stereo decoder and `out` buffer from the quick start:

```go
// Request exactly 20 ms: 960 samples per channel × 2 channels.
missingPCM := out[:960*channels]
samples, err := dec.Decode(nil, missingPCM)
if err != nil {
	log.Fatal(err)
}
concealed := missingPCM[:samples*channels]
fmt.Printf("concealed %d interleaved values\n", len(concealed))
// Consume concealed before reusing out for the next packet.
```

Passing the entire `MaxPacketSamples*Channels` buffer instead reuses the most
recent output duration, when available.

**Recover from the following packet.** If your jitter buffer can wait one packet,
use `nextPacket` to recover the loss first, then decode that same packet normally.
Do this instead of the concealment call above:

```go
// Allocate this alongside out, before the packet loop.
missingPCM := make([]float32, 960*channels)

// nextPacket follows the loss. Use its FEC, or PLC if it has no usable FEC.
recoveredSamples, err := dec.DecodeWithFEC(nextPacket, missingPCM, true)
if err != nil {
	log.Fatal(err)
}
recovered := missingPCM[:recoveredSamples*channels]

// The FEC call does not decode nextPacket's primary audio.
currentSamples, err := dec.Decode(nextPacket, out)
if err != nil {
	log.Fatal(err)
}
current := out[:currentSamples*channels]
fmt.Printf("play %d recovered values, then %d current values\n", len(recovered), len(current))
// Deliver recovered before current, and consume both before reusing their buffers.
```

In-band FEC is available in SILK/Hybrid packets when the sender includes it;
enabling FEC does not guarantee redundancy in every packet. At native 96 kHz,
`DecodeWithFEC(..., true)` uses concealment and ignores the supplied packet.
Run the complete [packet-loss example](examples/packet-loss) with
`go run ./examples/packet-loss`.

### Encoder controls

Set controls before encoding. For a voice stream, for example:

```go
if err := enc.SetBitrate(32000); err != nil { // bits/second across all channels
	log.Fatal(err)
}
if err := enc.SetComplexity(5); err != nil { // 0–10; higher spends more CPU
	log.Fatal(err)
}
enc.SetVBR(true) // let packet sizes vary with the audio
enc.SetDTX(true) // reduce traffic during silence

enc.SetFEC(true)                              // allow redundancy in SILK/Hybrid packets
if err := enc.SetPacketLoss(10); err != nil { // estimated loss percentage, 0–100
	log.Fatal(err)
}
```

With DTX, `Encode` can return zero bytes with a nil error: there is no packet to
send. Send nonempty packets, including short one- or two-byte DTX packets.

The default bitrate is 64 kbps. When comparing with libopus, set the same bitrate
explicitly: libopus defaults to automatic selection. `Bitrate()` reports the
configured target, including `BitrateAuto`/`BitrateMax`, rather than libopus's
effective bitrate.

### Voice activity

`VAD` and `SpeechDetector` score mono PCM frame by frame without an encoder.
Each call takes exactly 10 or 20 ms of samples, advances the detector's state,
and returns a score rather than a decision: choose the threshold and handle turn
timing in your application. Neither is safe for concurrent use, and `Reset` starts
a new stream. Construction can allocate; analysis calls do not.

- `VAD` runs the SILK voice detector at 8, 12, or 16 kHz and returns activity in
  Q8 (0–255). It follows level against an adaptive noise estimate.
- `SpeechDetector` runs the neural analysis inside the Opus encoder (libopus
  `activity_probability`) at 16, 24, or 48 kHz and returns a probability in
  [0, 1]. It scores spectral and temporal structure instead of level, so steady
  noise, hum, and clicks score low. Short bursts such as coughs, and other people
  talking, can score high.

```go
det, err := gopus.NewSpeechDetector(16000)
if err != nil {
	log.Fatal(err)
}

frame := make([]int16, 320) // 20 ms at 16 kHz; fill from your audio source
probability, err := det.AnalyzeInt16(frame)
if err != nil {
	log.Fatal(err)
}
talking := probability >= 0.5
```

The speech probability comes from the encoder's analysis code and matches libopus
for the same input. Its newest analysis window ends 10 ms before the newest
sample, and the first ten windows (about 200 ms) after construction or `Reset` are
warm-up. A window of exactly zero samples scores 0; the encoder repeats its
previous estimate there. A 10 ms frame completes a window on every second call and
repeats the previous result in between.

## Examples

Start with a complete encode/decode using reusable buffers:

```sh
go run ./examples/roundtrip-min
```

Run these from the repository root with `go run ./examples/<name>`.
Programs with command-line options accept `-h` for help.

| Example | Purpose |
|---|---|
| [roundtrip-min](examples/roundtrip-min) | Minimal caller-buffer encode/decode |
| [packet-loss](examples/packet-loss) | PLC and in-band FEC recovery ordering |
| [ogg-file](examples/ogg-file) | Ogg Opus reading, writing and seeking |
| [sample-rates](examples/sample-rates) | Int16 PCM at 8/12/16/24/48 kHz |
| [low-delay](examples/low-delay) | Short CELT frames and algorithmic delay |
| [repacketizer](examples/repacketizer) | Frame merging, splitting and padding |
| [surround](examples/surround) | Multistream 5.1 in Vorbis channel order |
| [roundtrip](examples/roundtrip) | Encode/decode quality across configurations |
| [encode-play](examples/encode-play), [decode-play](examples/decode-play) | Ogg encoding, WAV decoding and optional playback |
| [ffmpeg-interop](examples/ffmpeg-interop) | Interoperability with ffmpeg and ffprobe |
| [mix-arrivals](examples/mix-arrivals) | Timed speech mixing with loss and jitter |
| [bench-encode](examples/bench-encode), [bench-decode](examples/bench-decode) | File-based throughput estimates; see [Performance](#performance) |

Most examples use the default build. Optional APIs require their matching build
tag: QEXT uses `-tags gopus_qext`, DRED uses `-tags gopus_dred`, and OSCE uses
`-tags gopus_osce`. These runnable examples demonstrate API usage; they do not
imply that every optional feature and architecture has completed parity
validation. Build the in-module examples with `go build ./examples/...`.

For a local file round trip:

```sh
# Generate an Opus file, then decode it to 16-bit PCM in a WAV file.
go run ./examples/encode-play -duration 1 -out demo.opus
go run ./examples/decode-play -in demo.opus -out demo.wav
```

Playback is opt-in with `-play`; `decode-play -pipe` streams to `ffplay`.
The file round trip above needs no external audio tools. `ffmpeg-interop`
requires `ffmpeg` and `ffprobe`. `mix-arrivals` downloads its speech clips on
first use and caches them; its `-cache-dir` flag selects the cache directory.

Three examples are separate modules; run their commands inside their directories:

| Module | Command | Purpose |
|---|---|---|
| [external-consumer-smoke](examples/external-consumer-smoke) | `go test ./...` | Downstream public API checks |
| [webrtc-control](examples/webrtc-control) | `go run . -addr 127.0.0.1:8080` | Open `http://127.0.0.1:8080` for browser audio controls |
| [webrtc-dred-loopback](examples/webrtc-dred-loopback/README.md) | `go run .` | Desktop PLC/FEC/RED/DRED comparison; see its setup guide |

Check the example packages and nested projects without opening an audio device:

```sh
make test-consumer-smoke test-examples-smoke
```

The loopback checks use a test-only headless build tag. Running its desktop or
terminal demo requires the dependencies listed in its setup guide.

## Packages

| Package | API |
|---|---|
| [gopus](https://pkg.go.dev/github.com/thesyncim/gopus) | Single-stream codec, streaming reader/writer, multistream facade, packet parsing, repacketizer and controls |
| [multistream](https://pkg.go.dev/github.com/thesyncim/gopus/multistream) | Multistream codec and projection/ambisonics |
| [container/ogg](https://pkg.go.dev/github.com/thesyncim/gopus/container/ogg) | Ogg Opus reading and writing (RFC 7845) |
| [container/red](https://pkg.go.dev/github.com/thesyncim/gopus/container/red) | RTP RED payload construction, parsing and recovery (RFC 2198) |
| [types](https://pkg.go.dev/github.com/thesyncim/gopus/types) | Shared mode, bandwidth and signal enums |

The standard codec supports 8, 12, 16, 24 and 48 kHz PCM; SILK, CELT and Hybrid
modes; CBR, VBR and constrained VBR; and frame durations from 2.5 to 120 ms,
subject to mode constraints. Native 96 kHz and neural recovery use optional
build tags.

## Build options

Ordinary builds use scalar Go kernels. Go 1.27's `GOEXPERIMENT=simd` enables
`simd/archsimd` kernels on amd64 and arm64 where implemented; runtime CPU checks
select supported kernels, with scalar fallbacks. Apply the setting when building
your application, not just when fetching the dependency.

From the repository root:

```sh
go build ./...                                # ordinary scalar build
GOEXPERIMENT=simd go build ./...                # opt into SIMD
GOEXPERIMENT=simd go build -tags nosimd ./...    # force scalar with SIMD enabled
```

The benchmark commands explicitly apply the repository's `default.pgo`.
For an application, use `go build -pgo=/path/to/gopus/default.pgo` to select it.
Go's automatic PGO lookup searches the main package's directory; it does not
select a profile from a library dependency automatically.

The `nosimd` and `purego` tags both force scalar kernels and select the matching
scalar C reference in oracle tests. SIMD is opt-in and independent of the
optional codec features below.

Optional features mirror libopus build flags and are excluded from the default
build's import graph:

| Go build tag | libopus flag | Feature |
|---|---|---|
| `gopus_dred` | `--enable-dred` | DRED controls and standalone recovery |
| `gopus_osce` | `--enable-osce` (+ `ENABLE_DEEP_PLC`) | OSCE BWE/LACE/NoLACE and deep PLC |
| `gopus_qext` | `--enable-qext` | QEXT and native 96 kHz |
| `gopus_custom_modes` | `--enable-custom-modes` | Opus Custom modes |
| `gopus_fixed_point` | `--enable-fixed-point` | Integer CELT/SILK pipeline |

Default builds expose no optional extensions; `SetDNNBlob(...)` is a no-op
returning `ErrOptionalExtensionUnavailable`. DNN model loading follows libopus's
`USE_WEIGHTS_FILE` configuration. A build tag enables an implementation; it does
not establish parity for every feature, architecture or packet sequence.
Libopus rejects fixed-point combined with DRED/OSCE; those combinations have no
matching supported C reference lane.

| Extension | Availability | Probe |
|---|---|---|
| DNN blob loading | Available under `gopus_dred` / `gopus_osce` | `OptionalExtensionDNNBlob` |
| QEXT | Available under `gopus_qext` | `OptionalExtensionQEXT` |
| DRED | Available under `gopus_dred` (control + standalone) | `OptionalExtensionDRED` |
| OSCE BWE | Extra controls under `gopus_osce`; support probe returns false | `OptionalExtensionOSCEBWE` |

`SupportsOptionalExtension(OptionalExtensionOSCEBWE)` reports false: these
controls are exposed for parity work. DRED controls and standalone recovery
APIs also compile with `gopus_osce`, while the supported DRED probe follows
`gopus_dred`. Consult the [validation reference](reports/validation.md#coverage)
for tested feature combinations. Run tagged tests with the matching reference:

```sh
go test -tags gopus_qext ./...
go test -tags gopus_dred ./...
go test -tags gopus_osce ./...
```

## Performance

**Lower ns/sample or ns/packet means faster. Higher × realtime means faster.**
The Go/C time ratio divides Go time by C time: below `1.0` is a Go win; above
`1.0` is slower. For example, 287 ns/sample beats 348 ns/sample.

These measurements use **Apple M4 Max, darwin/arm64, Go 1.27.0, Apple Clang
21.0.0**, `GOMAXPROCS=1`, and identical C/Go workloads. Go explicitly uses the
checked-in **`default.pgo`** (SHA-256 `a85d5274259f…`; profile source `18eaa63b8`);
C uses `-O3` without PGO. The measured codec source is `42975dbd3`. Values are
medians across five runs of at least 300 ms. Every Go row reports **0 allocs/op**
after warmup; C allocations are not measured.

| Workload | C SIMD ns/sample | Go SIMD ns/sample | Go/C time | Result |
|---|---:|---:|---:|---|
| Encode aggregate | 146.32 | 143.66 | 0.982× | Go faster |
| Encode CELT, fullband, 20 ms stereo, 128 kbps | 74.57 | 86.89 | 1.165× | Go slower |
| Encode CELT, fullband, 5 ms mono, 64 kbps | 34.61 | 41.42 | 1.197× | Go slower |
| Encode SILK, wideband, 20 ms mono, 32 kbps | 337.21 | 282.86 | 0.839× | Go faster |
| Encode Hybrid, fullband, 20 ms mono, 64 kbps | 169.95 | 166.92 | 0.982× | Go faster |
| Encode Hybrid, fullband, 20 ms stereo, 96 kbps | 88.10 | 102.49 | 1.163× | Go slower |
| Decode RFC vectors, float32 | 19.00 | 17.63 | 0.928× | Go faster |
| Decode RFC vectors, int16 | 19.20 | 18.09 | 0.942× | Go faster |

With this profile, SIMD aggregate encoding takes **1.8% less time** than libopus;
SILK encoding takes **16.1% less**, and float32/int16 RFC decode take
**7.2%/5.8% less**. CELT 20 ms/5 ms encoding and stereo Hybrid take
**16.5%/19.7%/16.3% more time**. Small differences can include measurement noise.
The encoder aggregate weights five workloads equally by samples per channel;
it does not imply a win on every stream. RFC decode covers 20,075 packets.

The [measurement report](reports/benchmarks/2026-10-23-m4/README.md) includes
matched scalar results, the exact PGO hash and provenance, raw output, and
separate PGO-off measurements. Those snapshots do not isolate the effect of PGO.
The PGO-off short-frame decode comparison covers eight CELT 2.5 ms cases with a
Go/C geometric-mean ratio of 0.937× and public **0 B/op, 0 allocs/op** checks.

Native amd64 evidence is separate. The
[AMD EPYC 7763 measurements](reports/validation.md#performance) at `4b660d668`
use Go 1.27.1, GCC 13.3.0 and `GOAMD64=v3`. Their direct C comparison tools use
`-pgo=auto` without a package-local profile, so those particular rows have PGO
off. The ARM64 measurements above do **not** establish native amd64 performance
or SIMD parity for this revision. The
[v1/v2/v3 audit](reports/validation.md#amd64-compiler-targets) records native
compiler-target evidence; routine PR CI does not run that timing matrix.

Reproduce the scalar and SIMD comparisons from the repository root:

```sh
GOMAXPROCS=1 GOEXPERIMENT=nosimd make bench-libopus-guard \
  PGO_FILE=default.pgo BENCH_LIBOPUS_GUARD_TIME=300ms BENCH_LIBOPUS_GUARD_COUNT=5
GOMAXPROCS=1 GOEXPERIMENT=simd make bench-libopus-guard \
  PGO_FILE=default.pgo BENCH_LIBOPUS_GUARD_TIME=300ms BENCH_LIBOPUS_GUARD_COUNT=5
```

Both commands explicitly apply the same profile and enforce zero warm Go
allocations. Use `PGO_FILE=off` for a separate PGO-off run. Their runtime-ratio
limit is a regression guard, not a requirement to beat C. On a supporting amd64
CPU, prefix both commands with `GOAMD64=v3 GOPUS_LIBOPUS_AMD64_TARGET=v3`. Also
set `LIBOPUS_ENABLE_SCALAR=1` for the nosimd command or `LIBOPUS_ENABLE_SIMD=1`
for the SIMD command; leave the other reference flag unset. Omit the AMD64 target
settings on ARM64. Avoid concurrent builds or tests during timings.

These tools exclude file I/O, codec construction and C helper startup. The
file-based `bench-encode` and `bench-decode` examples include `opus_demo` startup
and file I/O in C timings, so they provide rough estimates.

## Parity & testing

The [parity contract](reports/validation.md#parity-contract) requires exact
API/protocol behavior and integer arithmetic, matching entropy ranges for
identical packets and history, and preservation of established exact regressions.
C and Go use the same features, controls, scalar widths and effective CPU
instructions. SIMD Go versus scalar C is not a parity comparison.

Floating-point allowances require a reproducible rounding cause, a narrow
kernel-specific bound, independent quality/recovery evidence and a reviewed
executable regression. Unexplained mismatches remain failures. Exact oracle
checks and real-audio `opus_compare` quality checks complement each other;
neither proves every possible input and state sequence.

The [coverage audit](reports/validation.md#coverage) records the build
configurations, inputs, and state sequences covered by exact comparisons,
and identifies evidence gaps. The documented
[C reference boundary](reports/validation.md#reference-boundary) is an unsafe
custom-QEXT history read at 96 kHz / 2,048 samples; Go uses bounded concealment
and compares defined C behavior.

Run checks from the repository root. The full suites build the pinned libopus
reference automatically; the library itself needs no C toolchain.

| Command | Checks |
|---|---|
| `make test-fast` | Short iteration suite; omits parity and exhaustive tiers |
| `make test` | Full default suite with live C oracles |
| `make test-simd` | Full opt-in SIMD suite with matching C oracles |
| `make test-race` | Fast suite with Go's race detector |
| `make test-build-contract` | Build tags and optional-feature boundaries |
| `make test-type-parity` | Codec storage widths against the tracked baseline |

Allocation regressions use warmed `testing.AllocsPerRun` checks with an exact
zero-allocation requirement for caller-buffer hot paths. See
[CONTRIBUTING.md](CONTRIBUTING.md) for quality, performance and release checks.

<details>
<summary>Test execution and CI reproducibility</summary>

Independent composite encode cases run concurrently, capped at four active cases
and `GOMAXPROCS`, with fresh codec state and every selected frame. Use
`-parallel=1` to serialize their subtests. C oracle helper binaries reuse a digest
of compiler identity, effective flags, preprocessed sources, and linked archive
contents; each input still executes the live C helper. Builds with custom linker
outputs bypass reuse.

Malformed multistream and projection sweeps send up to 128 independent requests
per C helper process. Each request creates a fresh Go decoder and a fresh C
decoder, including rejected packets; accepted results compare sample counts,
PCM bits, and final ranges. PCM files use chunked little-endian writes, and the
quality comparator serializes each request into one sized buffer. Independent
end-to-end conformance cells run within Go's test parallelism bound. Encoder
differentials reuse deterministic input templates and give every case a private
PCM copy; shared oracle payloads append PCM in one bounded slice operation.

`GOPUS_TEST_SHARD=INDEX/TOTAL` partitions the runnable suite by top-level test,
fuzz seed target, and example names. Each shard executes with `-count=1` and
emits its complete inventory with JSON results. `tools/aggregate_go_test_shards.py`
requires every assigned test to complete exactly once before merging the suite.
CI runs independent correctness lanes and four full-suite shards concurrently;
paired benchmark samples for both revisions share one runner.

</details>

## Trust and verification

The API is pre-v1.

`v0.1.0` is retracted: it has no published GitHub Release.
The [v0.2.3 release](https://github.com/thesyncim/gopus/releases/tag/v0.2.3)
includes the verification summary and logs.

Required branch checks:

<!-- required-checks:start -->
- `lint-static-analysis`
- `test-linux`
- `perf-linux`
- `test-macos`
- `test-windows`
<!-- required-checks:end -->

These checks build the pinned C reference with matching features and instructions
and run mandatory parity suites on Linux, macOS and Windows. The release workflow
requires green checks on the tagged commit; `make release-evidence` must also
produce a PASS summary with safety, performance and build provenance.

Security reports: [SECURITY.md](SECURITY.md). External API verification:
[examples/external-consumer-smoke/smoke_test.go](examples/external-consumer-smoke/smoke_test.go).
Contributions follow [CONTRIBUTING.md](CONTRIBUTING.md) and the
[code of conduct](CODE_OF_CONDUCT.md). See [LICENSE](LICENSE) for licensing.
