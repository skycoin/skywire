# Agent Instructions

gopus is a pure-Go, no-cgo implementation of the Opus audio codec (RFC 6716 /
RFC 8251) that targets **strong behavioral and quality parity with libopus 1.6.1**,
with scoped byte-exact guarantees under `reports/validation.md#parity-contract`. The
pinned reference lives in `tmp_check/opus-1.6.1/`; when behavior is uncertain,
gopus matches libopus unless fixture evidence says otherwise.

State current behavior in the present tense — in code comments and in these docs.
Do not write historical or changelog narrative ("previously…", "was a…", "now
uses…", "removed…"); describe what the code does today.

## Prime directive: parity, proven against a live C oracle

- Follow `reports/validation.md#parity-contract`: exact API/protocol behavior, integer
  primitives and conversions on identical inputs, and
  same-packet entropy ranges; preserve established byte-exact coverage. Universal
  floating-point or encoder packet identity across compiler targets is not a
  release requirement.
- **Never weaken a gate to make work pass.** Do not relax quality thresholds,
  edit fixture or baseline files, or skip a failing case to go green. A numerical
  allowance requires a localized rounding cause, a justified bound, independent
  quality/recovery evidence and an executable scoped regression under the parity
  target. Unknown mismatches remain failures. Fixture/baseline edits are
  review-visible evidence, not a shortcut.
- Parity is proven on two tiers (see README "Parity & testing"): bit-exact kernel
  oracles plus differential fuzzing of every public decode entry point, and
  `opus_compare` quality on real audio. Exact packet, range, and sample gates
  use matching C build configurations; quality scores complement those gates.

## Paired scalar and SIMD references

- Go 1.27 is the minimum version. All codec kernels are Go implementations.
- The ordinary build uses scalar Go. The `nosimd` and `purego` build tags force
  that path, including when `GOEXPERIMENT=simd` is set.
- `GOEXPERIMENT=simd` selects Go `archsimd` kernels where implemented, with
  scalar fallbacks for other kernels.
- Compare Go SIMD with libopus SIMD and Go scalar with libopus scalar on the
  same CPU, using identical input, application, controls, and scalar widths.
  Verify effective kernel dispatch as well as build flags and runtime features.
- Same-path exact tests retain their assertions. Validated floating-point
  differences use explicit per-kernel bounds under the parity target, never
  architecture-wide ULP waivers. Different libopus SIMD/scalar results cannot
  justify a mismatch against the matching reference.
- Prioritize semantic correctness, quality and zero allocations. Do not add
  compiler-specific hot-loop calls solely for last-bit identity once a numerical
  difference is validated. A measured 1–2% end-to-end variation is acceptable.
- Validated coverage, reference exceptions, and measured performance are recorded
  in `reports/validation.md#performance`. Passing a subset of tests does not
  prove complete parity.

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
- Run `go test` for the packages you touch in the ordinary build and with
  `GOEXPERIMENT=simd`; also run `-tags nosimd` when validating the scalar
  reference lane.

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
