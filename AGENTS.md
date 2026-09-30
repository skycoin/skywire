# AGENTS.md

Guidance for AI coding agents working in this repository.

These rules are combined from the agent and AI policies of the upstream
projects this one contributes to and depends on.

- tinygo-org/tinygo and tinygo-org/net, `AGENTS.md`
- mvdan/sh, `AGENTS.md`
- u-root/u-root, `AI_POLICY.md`, itself adapted from Delve and Ghostty

## There are humans here

Every issue, pull request and review comment is read by a person who is
donating the time to read it. Work that has not been checked moves the cost of
checking it onto them.

## Communication style

Use simple direct language everywhere outside the code. That includes commit
messages, pull request titles and descriptions, issues, and review comments.

Lead with the change and the reason for it. Two or three short paragraphs is
usually the whole description. Include a measurement, a table or a log excerpt
only where it is the evidence for a claim, not one for every claim.

Avoid extra colons, semicolons and dashes. Write plain sentences instead. AI
writing is verbose by default and adds noise that hides the point, so trim it
before it leaves the machine.

## Comments

Omit redundant comments. Where a comment is needed, keep it to two lines.

Where a comment records a specific requirement, cite the source, including the
section number, page number or URL.

## No coding tool attributions

Never add `Co-Authored-By`, a "Generated with" footer, or any other coding tool
attribution to a commit, pull request, issue or comment.

Some projects require AI assistance to be disclosed. They ask for free-form
prose in the pull request body, which is what to write. Still never a trailer.

## Verify before you submit

Run the change. Do not submit code that is only plausibly correct.

Never write code for a platform or environment you cannot test on. Where
something was not verified, say so plainly rather than implying it was.

Where a real implementation is the oracle, check against it rather than against
your own reasoning. For this repository that means a live visor, and for a
network change a second visor to talk to.

## Commits

The subject line is `package: lowercase summary` with no trailing period. Use a
comma separated list for several packages, and `all:` for a repo wide change.

Any behavior change gets a body, wrapped at about 72 columns, giving the
symptom, the cause and the fix. `Fixes #N` closes an issue and `For #N`
references one without closing it.

Organize a change into small commits that each build and pass the tests. A
commit whose only purpose is to fix up an earlier one in the same branch does
not belong in the final history.

## Pull requests

Avoid force pushing while a review is in progress, because it breaks the
conversation. Push fixup commits instead, then rebase once and force push a
single time when the review is done.

Load an issue's comments before acting on it, not just its description.

Branch from `develop`. Never push to `develop` directly.

## Working in this repository

`go build .` at the repository root builds the single binary that holds every
subcommand. Do not use `go build ./...`, which builds the same code many times
over.

`make check` runs the linters, `go vet`, the config generation and help smoke
checks, and the tests. It serializes itself with a lock file, so two concurrent
runs will not fight over the machine.

`make format` fixes import order and formatting. `make lint` needs
`make install-linters` first.

`make build-wasm` compile checks the js/wasm module, and `make build-ui` builds
the hypervisor UI.

Do not add a `replace` directive to `go.mod`. Import a fork by its own module
path instead.

Prefer `skywire cli visor state` and the other CLI queries over grepping a log
file. The CLI is the supported way to read a running visor.
