# Contributing to gopus

Thanks for helping improve `gopus`.

## Before You Open an Issue

- Bug reports: use the bug report template and include a minimal reproduction.
- Feature ideas: use the feature request template and describe the API or behavior you want.
- Usage questions or docs gaps: use the support question template and point to the code or documentation that blocked you.
- Security issues: follow [SECURITY.md](SECURITY.md) and do not report them publicly.

Maintainer triage is best effort. Small, reproducible reports and narrowly scoped pull requests move fastest.

## Project Expectations

Please keep the project priorities in mind:

1. Parity with libopus in quality and features
2. Performance
3. Maintainability
4. Documentation
5. Dead-test cleanup

When a change touches codec behavior:

- Cross-check codec math and bitstream decisions against libopus 1.6.1 before changing behavior.
- Prefer matching libopus over heuristic fixes unless fixture evidence justifies a divergence.
- Match libopus scalar widths for runtime state, signal buffers, and scratch buffers. Run `make test-type-parity`; do not refresh the baseline to hide new `float64`/`complex128` debt.
- Preserve zero allocations in the real-time encode/decode hot paths.
- Treat `testvectors/testdata/` and `tmp_check/` as fixed references unless the change is explicitly about fixtures or the pinned libopus snapshot.

For docs and public-facing material:

- Keep the README current; it is the single source of truth for support claims,
  release state, verification gates, and required CI checks.
- Keep extra Markdown focused on public policy or runnable examples.

## Verification

Run focused checks for the area you touched while iterating.

Common commands:

```bash
go test ./...
make test-type-parity
make test-quality
make bench-guard
make verify-production
```

Notes:

- `make ensure-libopus` bootstraps the pinned libopus 1.6.1 reference used by parity and quality checks.
- Some validation paths expect `ffmpeg` and `opusdec` to be available.
- Docs-only changes do not need the full codec verification bundle, but please sanity-check links, commands, and examples.

## Pull Requests

Please keep pull requests scoped and change-focused.

Good pull requests usually include:

- A short problem statement
- The user-visible or codec-visible behavior change
- Focused tests or fixtures when behavior changes
- The commands you ran to validate the change

Keep branch names, commit messages, and PR titles generic and descriptive.

## Community

By participating in this project, you agree to follow [CODE_OF_CONDUCT.md](CODE_OF_CONDUCT.md).
