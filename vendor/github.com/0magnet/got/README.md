# got

An HTTP client that downloads a file in concurrent byte ranges.

A fork of [melbahja/got](https://github.com/melbahja/got), extracted from
[skywire](https://github.com/skycoin/skywire), where it lived as `pkg/got`
and backs the `skywire-cli got` command. Skywire now imports it from here.

```go
g := got.New()
g.ProgressFunc = func(d *got.Download) {
	fmt.Printf("\r%d / %d bytes  %s/s", d.Size(), d.TotalSize(), humanize(d.Speed()))
}
if err := g.Download("https://example.com/big.iso", "/tmp/big.iso"); err != nil {
	log.Fatal(err)
}
```

## How a download runs

The first request is a one-byte range probe, `Range: bytes=0-0`, and what
comes back decides everything after it.

If the server answers with a `Content-Range`, the file is rangeable. The size
is read out of that header, the file is split into chunks, and the chunks are
fetched concurrently into one output file that has already been truncated to
its final length — each chunk writes at its own offset through an
`OffsetWriter`, so there are no part files to concatenate afterwards and no
second pass over the data.

If the server ignores the range and sends the whole body, the probe response
*is* the download. It streams to disk right there rather than being discarded
and refetched, so a server without range support costs one request, not two.

Concurrency and chunk size are derived from the file size and `NumCPU` when
left at zero, and either can be pinned. Each chunk retries up to `MaxRetries`
(3 by default) on its own, so one failed range does not fail the download.

## Resuming

With `Resume` set, chunk completion is journaled to a state file beside the
output. On a later run the journal is reloaded — after checking the URL still
matches — and only the chunks that never finished are fetched again. The
journal is removed once the download completes.

Resume is off by default, because it is only sound when the remote file has
not changed underneath it.

## SOCKS5

`NewWithProxy` takes a proxy address in three forms, and draws the same
distinction curl does between the two schemes:

| form | who resolves the destination hostname |
| --- | --- |
| `socks5h://host:port` | the proxy (`curl --socks5-hostname`) |
| `socks5://host:port` | this client, before dialing (`curl --socks5`) |
| `host:port` | the proxy — bare form is treated as `socks5h://` |

The distinction is not cosmetic. A proxy that resolves names no DNS server
knows — skywire's `dmsgweb` serves `<pk>.dmsg` hostnames this way — only works
over `socks5h`, because that is the mode in which the hostname is handed to
the proxy rather than looked up first. Under `socks5://`, an IP literal
destination is passed straight through and only real hostnames take the
resolver detour.

## The rest of the API

A downloader needs an HTTP request layer anyway, so the plain one is exported
rather than hidden:

- `Got.Request` — any method, with headers and a body, response written to an
  `io.Writer`.
- `NewRequest`, `ParseHeaders` (`"Key: Value"` strings), `NormalizeURL`
  (defaults a missing scheme to https), `GetFilename`.
- `Download` fields and getters for progress reporting: `Size`, `TotalSize`,
  `Speed`, `AvgSpeed`, `TotalCost`.

A filename from a `Content-Disposition` header is rejected if it contains
`..`, `/` or `\`, so a hostile server cannot steer the write outside the
output directory.

## Notes on the fork

Beyond the SOCKS5 support and the request helpers, the parts most changed
from the original are about what happens concurrently:

- Chunk-completion writes and the journal marshal are under one mutex; the
  progress-stop flag and the byte counters are atomic. The chunk goroutines
  and the progress goroutine both touch that state.
- `Got.Do` propagates the receiver's `Client` and context into a `Download`
  built by hand. Without it a hand-built download silently fell back to
  `DefaultClient()`, which is how a configured SOCKS5 proxy could end up
  bypassed — the destination resolved locally instead of through the dialer
  `NewWithProxy` had wired up.
- `Do` stops the progress goroutine and waits for it before returning, so no
  `ProgressFunc` call outlives the call that started it.

## License

MIT — see [LICENSE](LICENSE). Copyright (c) 2019 Mohamed Elbahja for the
original, (c) 2026 Moses Narrow for this fork.
## Dependency Graph

Made with [goda](https://github.com/loov/goda):

```
go run github.com/loov/goda@latest graph github.com/0magnet/got/... | dot -Tsvg -o docs/got-goda-graph.svg
```

![Dependency Graph](docs/got-goda-graph.svg "github.com/0magnet/got Dependency Graph")

## Lines of Code

Made with [gocloc](https://github.com/hhatto/gocloc) (excludes `vendor/`, `node_modules/`, `.git/`):

```
gocloc --not-match-d='(vendor|node_modules|\.git)' .
```

```
-------------------------------------------------------------------------------
Language                     files          blank        comment           code
-------------------------------------------------------------------------------
Go                               8            217            174           1067
Markdown                         1             31              0            104
YAML                             1              0              7             98
Makefile                         1             19             34             85
Bourne Shell                     1              8             16             30
-------------------------------------------------------------------------------
TOTAL                           12            275            231           1384
-------------------------------------------------------------------------------
```
