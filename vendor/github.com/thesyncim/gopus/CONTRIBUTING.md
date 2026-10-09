# Contributing to gopus

Use a minimal reproduction for bugs and describe the intended API or behavior
for feature requests. Include the build tags, Go version, CPU target, input and
controls needed to reproduce codec differences. Report security issues through
[SECURITY.md](SECURITY.md).

## Codec changes

The pinned reference is libopus 1.6.1 in `tmp_check/opus-1.6.1/`. Follow the
[parity contract](reports/validation.md#parity-contract): preserve exact API,
protocol, integer and established packet/range/PCM checks. An unexplained
floating-point difference remains a failure; accepting a narrow numerical bound
requires first-divergence, quality/recovery and executable regression evidence.
Document undefined C behavior with its source and reproducer.

- Pair scalar Go with scalar C and SIMD Go with C using the same effective
  instructions, feature flags, input, controls and stream history.
- Match libopus scalar widths in state, signal and scratch storage. Run
  `make test-type-parity`; do not refresh its baseline to hide new debt.
- Preserve zero steady-state allocations. Warm new hot paths and require
  `testing.AllocsPerRun(...) == 0` for their caller-buffer use.
- Keep fixtures and the pinned reference fixed unless the change explicitly
  updates them with reviewable evidence. Fix causes rather than weakening gates.
- Measure runtime changes against matched builds and record the revision,
  compiler target, dispatch and benchmark method.

## Source documentation

Use Go doc comments that begin with the declared name. Explain behavior callers
need: units, channel layout, buffer ownership, valid inputs, returned counts,
errors and state changes. Link related identifiers with Go doc links. Put
examples in example tests and keep package comments focused on their APIs.

Internal comments should explain invariants or numerical choices. Cite the C
helper when an unusual width, rounding boundary or operation order is required.
Avoid repeating the implementation or narrating its history. Run `gofmt` on Go
files you change.

The README is the entry point for usage, support and release state.
[reports/validation.md](reports/validation.md) holds the correctness contract,
coverage and performance evidence; keep its measurements tied to their actual
revisions. Add separate Markdown only for a distinct policy or runnable example.

## Verification

Run focused package tests during iteration. For codec changes, test scalar and
SIMD builds and applicable feature tags against their matching C references.
`make ensure-libopus` builds the pinned reference; some quality paths also need
`ffmpeg` and `opusdec`.

```sh
go test ./...
make test-build-contract
make test-type-parity
make lint
make test-consumer-smoke
make test-examples-smoke
make test-quality
make bench-guard
make verify-production
make verify-production-exhaustive
make release-evidence
```

Feature-specific checks include `make test-dnn-blob-parity`,
`make test-qext-parity`, `make test-dred-tag`, `make test-extra-controls-parity`
and `make test-custom-parity`. Documentation changes need link, example and
contract checks; they do not require the full codec bundle.

Before publishing a tag, its commit must pass the required checks listed in the
README and `make release-evidence` must produce a PASS summary.

## Pull requests and development hooks

Keep changes focused. Describe the problem, observable behavior and verification
commands; include regression tests for behavior changes. Use descriptive branch
names and commit messages.

To enable the repository's commit-message hook:

```sh
git config core.hooksPath .githooks
chmod +x .githooks/prepare-commit-msg
```

The hook removes the Cursor agent co-author trailer before finalizing a commit.
Participation follows [CODE_OF_CONDUCT.md](CODE_OF_CONDUCT.md).
