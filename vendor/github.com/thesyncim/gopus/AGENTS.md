# Agent Instructions

gopus is a pure-Go, no-cgo implementation of the Opus audio codec (RFC 6716 /
RFC 8251) that targets **byte- and quality-parity with libopus 1.6.1**. The
pinned reference lives in `tmp_check/opus-1.6.1/`; when behavior is uncertain,
gopus matches libopus unless fixture evidence says otherwise.

State current behavior in the present tense — in code comments and in these docs.
Do not write historical or changelog narrative ("previously…", "was a…", "now
uses…", "removed…"); describe what the code does today.

## Prime directive: parity, proven against a live C oracle

- Match libopus 1.6.1 exactly — both observable behavior and, on the bit-exact
  lanes, the emitted bytes and the entropy coder's final range.
- **Never weaken a gate to make work pass.** Do not relax quality thresholds,
  edit fixture or baseline files, loosen oracle tolerances, or skip a failing
  case to go green. Fix the root cause. Fixture/baseline edits are review-visible
  evidence, not a shortcut.
- Parity is proven on two tiers (see README "Parity & testing"): bit-exact kernel
  oracles plus differential fuzzing of every public decode entry point, and
  `opus_compare` quality on real audio. SILK decode is bit-exact; CELT/Hybrid sit
  in the near-exact envelope.

## Tiers and the per-arch float budget

- The `purego` build tag is the scalar reference path: **bit-exact on every
  architecture**, and the lane the byte-parity gates compare against.
- The default build selects assembly (arm64 NEON, amd64 SSE/AVX2) only where
  libopus does, and only behind a quality gate.
- One residual is documented: a few CELT float kernels drift by ≤1 ULP on
  darwin/arm64 — a per-arch float budget, exactly like libopus's own
  NEON-vs-scalar difference. Do not chase ≤1-ULP arm64 float drift as a bug.

## Libopus type parity

Runtime codec-domain storage must use the same scalar width as libopus, including
temporary scratch buffers.

- In the libopus float build, `opus_val16`, `opus_val32`, `opus_val64`,
  `opus_res`, `celt_sig`, `celt_norm`, `celt_ener`, `celt_glog`, `celt_coef`, and
  `silk_float` are C `float`; use Go `float32` or the matching local alias.
- Scratch is in scope. Reusable scratch fields, local temporary slices,
  conversion buffers, MDCT/PVQ/energy/PLC/QEXT/DRED buffers, SILK pitch/NLSF/NSQ
  state, helper allocators, and test-only runtime probes must follow libopus
  width too.
- Do not add runtime `float64`, `complex128`, `KissFFT64State`,
  `ensureFloat64Slice`, or `ensureComplexSlice` unless the matching libopus source
  uses C `double` for that exact helper. Cite the C file/function in the code or
  the baseline reason.
- Use fixed-width Go integer types for libopus state and arithmetic: C
  `int`/`opus_int` runtime fields and scratch should be `int32`, `opus_int16`
  should be `int16`, `opus_uint32` should be `uint32`, and so on.
- Go `int` is allowed for indexes, lengths, loop counters, slice capacities,
  public Go ergonomics, and table indexing only. Do not keep reusable scratch or
  codec-domain arithmetic in `int` just because it compiles.
- When a remaining wide scalar or generic `int` is deliberate, it must be an
  index/length/public-boundary use or have a source-cited reason tied to a
  matching libopus type.

## Allocation discipline

Encode/decode and the container hot paths are allocation-free in steady state via
caller-owned, pre-allocated buffers. Keep them that way, and lock any new
zero-alloc path with a `testing.AllocsPerRun(...) == 0` test (warm up first so the
measurement reflects steady state, not one-time lazy init).

## Style and API

- Write idiomatic Go that reads like the surrounding code. Match libopus logic and
  numeric behavior exactly, but do not transliterate C line-for-line.
- The public API is pre-v1 and unreleased: break internal or public interfaces
  when it yields a cleaner design, then fix the callers. Do not add conversion
  bridges, compatibility wrappers, or cast-heavy shims to preserve an old surface.

## Build and test

- `make test` runs the full suite against a live libopus oracle (`ensure-libopus`
  builds the pinned reference). `make test-fast` is the quick lane; `make
  test-race` is the race sweep.
- `make test-type-parity` runs the type-parity guard. Do **not** run `make
  update-type-parity-baseline` to hide new debt; refresh the baseline only when
  cleanup removed findings or a remaining wide scalar is intentionally tied to a
  specific C helper.
- `make lint` (golangci-lint + vet across the build-tag matrix) and `make
  deadcode` (the multi-config dead-code detector — a single-config `deadcode` run
  is false-positive dominated here because of build-tag/arch gating).
- Optional features are behind build tags, mirrored tag-for-flag with libopus:
  `gopus_dred`, `gopus_osce`, `gopus_qext`, `gopus_custom_modes`,
  `gopus_fixed_point`. The default build links zero of their code.
- Run `go test` for the packages you touch (default and, where relevant, `-tags
  purego`) before finishing a codec or runtime change.

## Layout

- `internal/{celt,silk,hybrid,encoder}` — the codec core.
- `internal/{dred,osce,lpcnetplc,dnnmath,dnnblob}` — the tag-gated neural features
  (DRED, OSCE/deep-PLC).
- `internal/{rangecoding,opusmath,plc,fixedpoint,util}` — shared primitives;
  `internal/libopustest` — the C oracle helper harness.
- Public packages: root `github.com/thesyncim/gopus`, `multistream`, `types`,
  `container/ogg`, `container/red`.
- `tmp_check/opus-1.6.1/` — pinned libopus reference; `testvectors/` — RFC 8251
  vectors; `tools/` — C oracle/reference sources; `scripts/` — dev tooling.
