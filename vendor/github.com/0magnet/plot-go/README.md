# plot-go

A Go port of [annacrombie/plot](https://github.com/annacrombie/plot) — line
graphs on the command line — built on
[asciigraph](https://github.com/guptarohit/asciigraph) for the drawing.

**[Live demo](https://0magnet.github.io/plot-go/)** — two counters the tab actually has, followed and redrawn in a terminal. The pipeline comes from the query: [`?p=roc:5`](https://0magnet.github.io/plot-go/?p=roc:5), [`?p=avg:5|roc:5`](https://0magnet.github.io/plot-go/?p=avg:5%7Croc:5), [`?p=cma`](https://0magnet.github.io/plot-go/?p=cma).

![plot-go in the browser](docs/plot-go-demo.png "frame interval and event-loop lag, smoothed and plotted live into an xterm-go terminal")

There is no `/sys` in a browser, so the demo follows what a tab does have:
the gap between animation frames, and how late a zero-millisecond timeout
runs. Both climb when the event loop is busy, which is the same reason
`plotnet` watches `rx_packets` and `tx_packets`. It draws into
[xterm-go](https://github.com/0magnet/xterm-go) — the terminal
[websh](https://github.com/0magnet/websh) runs on — because asciigraph's
output *is* a terminal frame: the colors are SGR escapes and the redraw is a
cursor-home and a clear.

```
$ seq 0 30 | awk '{print $1*$1*3 + 1000}' | plot-go -d 4
 3700.00 ┤                          ╭───
 3025.00 ┤                     ╭────╯
 2350.00 ┤               ╭─────╯
 1675.00 ┤  ╭────────────╯
 1000.00 ┼──╯

$ seq 0 30 | awk '{print $1*$1*3 + 1000}' | plot-go -d 4 -p roc:1
 177.00 ┤                          ╭───
 132.75 ┤                  ╭───────╯
  88.50 ┤           ╭──────╯
  44.25 ┤    ╭──────╯
   0.00 ┼────╯
```

Same numbers both times. The second one is the point of this repository.

## What it is for

asciigraph already draws a series well. What it has no answer for is a series
that is not worth drawing yet — a monotonic counter, a noisy signal, more
samples than columns. plot's answer is a processing pipeline that runs before
anything is rendered, and that is what has been ported here.

A counter on a network interface is a line going up and to the right no matter
what the traffic does. Through `roc` it is throughput.

## Pipelines

`-p` takes processors separated by `|`, each with an optional `:argument`. The
output of one is the input of the next, so `avg:5|roc:5` averages every five
samples and then reports the rate of change of those averages.

| | argument | what it does |
|---|---|---|
| `avg` | n | averages every n samples into one |
| `sma` | n, odd | simple moving average over n samples — a boxcar filter |
| `cma` | none | cumulative average of everything so far |
| `roc` | interval | rate of change: `(value - previous) / interval` |

The names and syntax are the original's, so a command line written for C plot
means the same thing here.

## Input

Numbers are picked out of whatever the input happens to be — the scanner looks
for a digit and reads a number from there, skipping anything else. `load: 1.5
ok` is a data point.

That leniency has one edge worth knowing: a hyphen in front of digits is a
sign, so `[2026-01-01]` reads as `2026, -1, -1`. The original does the same.

Sources are named with `-i`, or left out to read standard input. Each may carry
flags after a colon:

```
plot-go -i /sys/class/net/eno1/statistics/rx_bytes:r -p roc:1 -f
```

`:r` re-reads the file from the start on every poll. It is the flag that makes
following a counter work at all, and the reason is worth stating: a log grows,
so following it means waiting at the end for more; a counter under `/sys` is a
single number that changes in place, so following it means going back to the
top. `-f` does the first, `:r` the second, and a network counter needs both.

`-A` follows as `-f` does but stops when the input ends, rather than waiting
there.

## Several sources at once

`-i` may be repeated. `-c` and `-p` bind to the source that comes *after* them,
so each series gets its own color and its own processing:

```
plot-go -f -S 500 -p roc:1 \
  -c l -i /sys/class/net/eno1/statistics/rx_packets:r \
  -c r -i /sys/class/net/eno1/statistics/tx_packets:r
```

```
 576.00 ┤  ╭─╮
 480.00 ┤  │ │
 384.00 ┤  │ │
 288.00 ┤╭───╮
 192.00 ┤│   ╰
  96.00 ┤│
   0.00 ┼╯

         ■ /sys/class/net/eno1/statistics/rx_packets   ■ /sys/class/net/eno1/statistics/tx_packets
```

A `-p` before any `-i`, as above, is the default for every source; a `-p` after
one belongs to the next source and is appended to the default.

This ordering is why the flags are parsed by hand rather than with the `flag`
package, which does not preserve the order flags were given in.

## Portability

No cgo and no syscalls beyond `os`, so it runs where Go runs. The same test
suite is run four ways:

```
make check          # host
make test-wasm      # js/wasm, under Node
make test-browser   # js/wasm, in a headless browser
make test-tinygo    # TinyGo, plus the wasip1 and wasm builds
```

TinyGo compiles it to a 1.1 MB wasm module, and to a native binary that polls a
`/sys` counter exactly as the Go build does.

Only `OpenSource` touches the host at all — files and standard input. Everything
above it (`Splitter`, `Pipeline`, the processors, and `NewSource` over any
`io.Reader`) is arithmetic, so a browser can feed it numbers from anywhere.

## Differences from the original

**The moving average keeps a flat signal flat.** C plot divides the warm-up sum
by one less than the number of samples in it, so a constant series of 10 with
`sma:5` comes out as 15, 13.33, 12.5, 10 — a spike that is not in the data.
Here it is 10 throughout. The emission schedule is unchanged: output still
starts once the window is more than half full.

**Chunking cannot change the result.** The processors keep their own state
rather than leaving samples in the caller's buffer to be offered again, so
reading a file in one go and polling a counter one sample at a time produce the
same numbers. In the original a window spanning two reads is counted twice;
it does not show up because a read usually fills the whole buffer in one go.

**The end of the data is not dropped.** C plot emits a block only once a sample
beyond it has arrived — its loop runs while `i + n < len` — so the last block
of a finished stream is never emitted. `seq 1 10 | plot -p avg:5` draws one
point where there are two. Here the stream is flushed when it ends, so both
come out, and a final block shorter than `n` is averaged over what it has.

**`-w` sets the plot width rather than the data's.** asciigraph resamples a
series to the width it is given; the original pads instead, drawing six points
in six columns of a forty-column plot. Same numbers, wider line.

## Crossings and x labels

`-m` merges the junctions where two series cross. Without it whichever line is
drawn second wins the cell, and a crossing reads as a break in the one
underneath:

```
 19.00 ┼────╮      ╰─╮    ╭──────────╮      ╰─╮      # plain
 14.50 ┤    ╰───╮    ╰╭───╯──╯       ╰───╮    ╰

 19.00 ┼────╮      ╰─╮    ╭──┬──┴────╮      ╰─╮      # -m
 14.50 ┤    ╰───╮    ╰┬───┴──╯       ╰───╮    ╰
```

This needed a change in asciigraph, which had no way to express it. It is
[`MergeSeries()` on a branch of the fork][merge-branch], which this repository
depends on by `replace` until it lands upstream.

`-x every:offset:mod` labels the x axis: one label every N columns, counting
from an offset, wrapping at a modulus — so `-x 10:0:60` counts seconds up to a
minute and starts again.

```
$ seq 1 41 | plot-go -d 3 -x 10
       └┬─────────┬─────────┬─────────┬─────────┬
        0        10        20        30        40
```

[merge-branch]: https://github.com/0magnet/asciigraph/tree/merge-series-crossings

## Flags the original has that mean less here

`-y` is accepted in its full `width:prec:side` form but only the precision
applies: asciigraph sizes the labels itself and puts them on the left.

`-x` takes only the first three of its five fields. The side the labels go on
and the color of the wrap point have no equivalent — asciigraph draws one
labeled axis, below the plot, in one color — so asking for them is an error
rather than being quietly given something else.

The tick spacing lands exactly every N columns when N divides the width, and as
near as the columns allow when it does not.

## License

MIT, as the original is. See [LICENSE](LICENSE).

## Dependency Graph

Made with [goda](https://github.com/loov/goda):

```
go run github.com/loov/goda@latest graph github.com/0magnet/plot-go/... | dot -Tsvg -o docs/plot-go-goda-graph.svg
```

![Dependency Graph](docs/plot-go-goda-graph.svg "github.com/0magnet/plot-go Dependency Graph")

## Lines of Code

Made with [gocloc](https://github.com/hhatto/gocloc) (excludes `vendor/`, `node_modules/`, `.git/`):

```
gocloc --not-match-d='(vendor|node_modules|\.git)' .
```

```
-------------------------------------------------------------------------------
Language                     files          blank        comment           code
-------------------------------------------------------------------------------
Go                              10            277            415           2144
Markdown                         1             57              0            168
YAML                             1              0              7             98
Makefile                         1             21             33             94
Bourne Shell                     1              8             16             30
-------------------------------------------------------------------------------
TOTAL                           14            363            471           2534
-------------------------------------------------------------------------------
```
