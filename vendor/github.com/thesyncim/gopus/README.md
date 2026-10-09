# gopus

gopus encodes and decodes Opus audio in pure Go, without cgo or an external
codec library. It supports voice and music, mono and stereo, multistream and
ambisonics, Ogg files, and RTP RED recovery.

The packet APIs use caller-owned buffers for allocation-free processing after
warmup. Ordinary builds use scalar Go; Go 1.27's experimental SIMD support adds
CPU-specific kernels. Correctness is checked against pinned libopus 1.6.1 and
the RFC 8251 test vectors; see [parity and testing](#parity--testing) for the
exact guarantees and tested configurations.

[API reference](https://pkg.go.dev/github.com/thesyncim/gopus) ·
[Quick start](#quick-start) · [Examples](#examples) ·
[Performance](#performance) · [Contributing](CONTRIBUTING.md)

## Install

This README describes `master`, which requires Go 1.27 or newer:

```sh
go get github.com/thesyncim/gopus@master
```

For the published release, use `@v0.2.2` and its
[versioned documentation](https://github.com/thesyncim/gopus/tree/v0.2.2).

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

Use one encoder and decoder per stream, and reuse their buffers. With `enc`,
`dec`, and `cfg` from the quick start, the same round trip works with `int16` PCM:

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

Ordinary builds use scalar Go kernels. `GOEXPERIMENT=simd` compiles
`simd/archsimd` kernels where implemented; runtime CPU checks select supported
kernels and the rest use scalar code. `-tags nosimd` or `-tags purego` forces
the scalar path, including the matching scalar C reference in oracle tests.
Enable SIMD when building your application; for this repository:

```sh
GOEXPERIMENT=simd go build ./...
```

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

The table pairs **C scalar with Go scalar** and **C SIMD with Go SIMD**, using
identical inputs and controls. Values are median **ns/sample per channel**
(lower is faster) on AMD EPYC 7763, Go 1.27.1 and GCC 13.3.0, with
**GOAMD64=v3**, PGO and candidate `4b660d668`. Each case has three runs of
at least 250 ms. Go reports zero allocations; C allocations are not measured.

| Workload | C scalar | Go scalar | C SIMD | Go SIMD |
|---|---:|---:|---:|---:|
| Encode CELT, fullband, 20 ms stereo, 128 kbps | 176.89 | 197.94 | 129.50 | 126.28 |
| Encode CELT, fullband, 5 ms mono, 64 kbps | 75.99 | 93.79 | 69.83 | 80.00 |
| Encode SILK, wideband, 20 ms mono, 32 kbps | 705.61 | 685.98 | 462.01 | 329.21 |
| Encode Hybrid, fullband, 20 ms mono, 64 kbps | 363.00 | 420.99 | 253.40 | 225.80 |
| Encode Hybrid, fullband, 20 ms stereo, 96 kbps | 200.37 | 227.97 | 145.10 | 139.57 |
| Decode RFC vectors, float32 | 38.22 | 42.29 | 35.90 | 33.14 |
| Decode RFC vectors, int16 | 42.23 | 46.80 | 38.83 | 37.64 |

Decoder rows aggregate 20,075 identical packets. Go SIMD takes 2.5–28.7% less
time than matched C SIMD in four encoder cases and 14.6% more in the 5 ms CELT
case. Float32 and int16 decode take 7.7% and 3.1% less time, respectively.
Results apply to these workloads and the stated revision; they are not a speed
guarantee for every stream or CPU.

The [validation reference](reports/validation.md#performance) contains all
**53 replacement routines**, assembly/Go/`nosimd` comparisons, raw-run links,
and measurements of later parity fixes. Its paired incremental measurement
records public encoder time changes within 0.5%; measurements from different
hosts are kept separate.

Use **GOAMD64=v3** on a supporting CPU and select the same C compiler target:

```sh
GOAMD64=v3 GOEXPERIMENT=simd GOPUS_LIBOPUS_AMD64_TARGET=v3 go run ./tools/encoderbenchcmp
GOAMD64=v3 GOEXPERIMENT=simd GOPUS_LIBOPUS_AMD64_TARGET=v3 go run ./tools/testvectorbenchcmp -cases aggregate
```

These tools measure codec work with matched C and Go workloads. The file-based
`bench-encode` and `bench-decode` examples include `opus_demo` process startup
and file I/O in C timings, so they provide rough estimates.

For scalar comparisons, retain both target settings and use `GOEXPERIMENT=nosimd`.
On ARM64, omit both AMD64 target settings. The optional
[v1/v2/v3 audit](reports/validation.md#amd64-compiler-targets) compares compiler
targets on one native host; routine PR CI does not run that timing matrix.

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

Use `make test-fast` for iteration, `make test` for the live C-oracle suite,
and `make test-build-contract` for build boundaries. See
[CONTRIBUTING.md](CONTRIBUTING.md) for the verification and release checklist.

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

## Trust And Verification

Released version: `v0.2.2`. The API is pre-v1.

`v0.1.0` is retracted: it has no published GitHub Release.
Latest release evidence: attached to the
[v0.2.2 release](https://github.com/thesyncim/gopus/releases/tag/v0.2.2).

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
