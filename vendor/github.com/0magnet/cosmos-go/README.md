<p align="center">
  <h1 align="center">🌌 cosmos-go</h1>
</p>
<p align="center">GPU-accelerated Force Graph — <a href="https://github.com/cosmograph-org/cosmos">cosmos.gl</a> ported to Go/WebAssembly</p>

cosmos-go is a full Go port of [cosmos.gl](https://github.com/cosmograph-org/cosmos) **2.6.3** (MIT), the WebGL force graph layout algorithm and rendering engine behind [Cosmograph](https://cosmograph.app) — and, in its 1.x form, the Skywire network visualizer. All computations and drawing happen on the GPU in fragment and vertex shaders (carried over verbatim from the original), avoiding expensive memory operations. It enables real-time simulation of network graphs consisting of hundreds of thousands of points and links on modern hardware.

The Go port drives the shaders through `syscall/js` with a raw-WebGL command layer (replacing `regl`), reimplements the d3-zoom / d3-drag interaction behaviors — including d3's smooth van Wijk–Nuij zoom transitions — and compiles with both the **standard Go toolchain** (`GOOS=js GOARCH=wasm`, ~3.4 MB) and **TinyGo** (`-target wasm`, ~610 KB).

[🎮 Live demo](https://0magnet.github.io/cosmos-go/) (compiled with TinyGo)

Ported feature set: GPU force simulation (many-body repulsion in two variants, springs, gravity, centering, mouse repulsion, **clustering forces**), pan/zoom, **point dragging**, **pinned points**, **9 point shapes**, **image atlas point sprites**, hover ring, focus ring, GPU picking for points **and links**, link arrows, curved links, rectangle **and polygon** selection with greyout, position tracking, viewport sampling, fit-view animations, and deterministic layouts via random seed.

### Quick Start

```bash
go get github.com/0magnet/cosmos-go
```

```go
package main

import (
    "syscall/js"

    cosmos "github.com/0magnet/cosmos-go"
)

func main() {
    div := js.Global().Get("document").Call("getElementById", "graph")

    cfg := cosmos.NewConfig()
    cfg.SimulationRepulsion = 1.0
    cfg.RenderHoveredPointRing = true
    cfg.EnableDrag = true
    cfg.OnPointClick = func(index int, pos [2]float64, event js.Value) {
        println("clicked point", index)
    }

    graph, err := cosmos.New(div, cfg)
    if err != nil {
        panic(err) // WebGL or a required extension is unavailable
    }

    // flat typed-array data, exactly like the JS API
    graph.SetPointPositions([]float32{4000, 4000, 4100, 4000, 4050, 4100}) // [x1,y1, x2,y2, ...]
    red := cosmos.GetRgbaColor("#fd7f6f") // colors are normalized 0..1 RGBA
    graph.SetPointColors([]float32{
        red[0], red[1], red[2], red[3],
        0.49, 0.69, 0.84, 1,
        0.70, 0.88, 0.38, 1,
    })
    graph.SetLinks([]float32{0, 1, 1, 2, 2, 0}) // [source1,target1, ...] point indices

    graph.Render()

    select {} // keep the Go runtime alive for the render loop
}
```

> **Note**
> `New` takes a container `div`; the canvas is created inside it (like cosmos.gl v2).
> WebGL 1 with the `OES_texture_float` and `ANGLE_instanced_arrays` extensions is required. Like the original, the Many-Body force also relies on float blending (`EXT_float_blend`), which iOS stopped exposing in 15.4.
>
> Unlike the 1.x line, cosmos v2 uses the positions you provide as-is (no random placement) — spread your initial positions around the space center (default space size 8192) or set `Config.RescalePositions`.

Build and serve:

```bash
GOOS=js GOARCH=wasm go build -o app.wasm . && cp "$(go env GOROOT)/lib/wasm/wasm_exec.js" .
# or, for a ~6x smaller binary:
tinygo build -o app.wasm -target wasm -no-debug . && cp "$(tinygo env TINYGOROOT)/targets/wasm_exec.js" .
```

See <a href="examples/demo">examples/demo</a> for a complete example with pan/zoom, hover, drag, selection, link hover and per-cluster point shapes, plus a build script for both toolchains.

### Data model

All bulk data uses flat `[]float32` slices mirroring the Float32Array API of cosmos.gl:

| Method | Format |
|---|---|
| `SetPointPositions([]float32, dontRescale ...bool)` | `[x1, y1, x2, y2, ...]` |
| `SetPointColors([]float32)` | `[r, g, b, a, ...]` normalized 0..1 (use `GetRgbaColor`) |
| `SetPointSizes([]float32)` | `[size1, size2, ...]` |
| `SetPointShapes([]float32)` | `ShapeCircle`, `ShapeSquare`, `ShapeTriangle`, `ShapeDiamond`, `ShapePentagon`, `ShapeHexagon`, `ShapeStar`, `ShapeCross`, `ShapeNone` |
| `SetImageData([]js.Value)` / `SetPointImageIndices` / `SetPointImageSizes` | JS `ImageData` objects packed into a texture atlas |
| `SetLinks([]float32)` | `[source1, target1, ...]` point indices |
| `SetLinkColors` / `SetLinkWidths` / `SetLinkArrows([]bool)` / `SetLinkStrength` | per-link values |
| `SetPointClusters([]int)` | cluster index per point, `-1` = unclustered |
| `SetClusterPositions([]float32)` | `[x1, y1, ...]`, negative = position at centermass |
| `SetPointClusterStrength([]float32)` | per-point cluster force coefficients |
| `SetPinnedPoints([]int)` | indices of fixed points (nil/empty = unpin all) |

Data setters are lazy; call `Render()` (optionally with an alpha value) to apply them and start rendering.

### Configuration

`cosmos.NewConfig()` returns a `*Config` with the same defaults as cosmos.gl 2.6.3. All configuration parameters are mirrored with Go naming — flat simulation parameters (`SimulationDecay`, `SimulationGravity`, `SimulationRepulsion`, `SimulationRepulsionTheta`, `SimulationLinkSpring`, `SimulationLinkDistance`, `SimulationFriction`, `SimulationCluster`, ...), appearance (`BackgroundColor`, `PointDefaultColor`, `PointDefaultSize`, `PointOpacity`, `LinkDefaultColor`, `CurvedLinks`, `LinkVisibilityDistanceRange`, ...), interaction (`EnableZoom`, `EnableDrag`, `EnableRightClickRepulsion`, `ScalePointsOnZoom`, `ScaleLinksOnZoom`, ...), view fitting (`FitViewOnInit`, `FitViewDelay`, `FitViewPadding`, `FitViewByPointIndices`, ...), plus `RandomSeed`, `PixelRatio`, `SpaceSize`, `UseClassicQuadtree`, `ShowFPSMonitor` and `Attribution`.

Event callbacks are config fields with Go signatures (point/link indices, `-1` = none): `OnClick`, `OnPointClick`, `OnLinkClick`, `OnBackgroundClick`, `OnMouseMove`, `OnPointMouseOver/Out`, `OnLinkMouseOver/Out`, `OnZoomStart/OnZoom/OnZoomEnd`, `OnDragStart/OnDrag/OnDragEnd`, and the simulation lifecycle `OnSimulationStart/Tick/End/Pause/Unpause`.

### API

Mirroring the cosmos.gl v2 API:

- `New(div js.Value, cfg *Config) (*Graph, error)`, `Render(alpha ...float64)`, `Destroy()`
- Simulation: `Start(alpha ...float64)`, `Stop()`, `Pause()`, `Unpause()`, `Step()`, `Progress()`, `IsSimulationRunning()`
- View: `Zoom` / `SetZoomLevel` / `GetZoomLevel`, `FitView`, `FitViewByPointIndices`, `FitViewByPointPositions`, `ZoomToPointByIndex`
- Readback: `GetPointPositions()`, `GetClusterPositions()`, `TrackPointPositionsByIndices` + `GetTrackedPointPositionsMap/Array`, `GetSampledPoints()`
- Selection: `GetPointsInRect`, `GetPointsInPolygon`, `SelectPointsInRect`, `SelectPointsInPolygon`, `SelectPointByIndex`, `SelectPointsByIndices`, `UnselectPoints`, `GetSelectedIndices`
- Misc: `GetAdjacentIndices`, `SetFocusedPointByIndex`, `SpaceToScreenPosition`, `ScreenToSpacePosition`, `SpaceToScreenRadius`, `GetPointRadiusByIndex`, `EnableZoom()` / `DisableZoom()`

Interactions match the original: drag to pan, mouse wheel to zoom, double-click to zoom in (shift + double-click to zoom out), drag points with the mouse when `EnableDrag` is set (hold Space to pan instead), and hold the right mouse button to repel points when `EnableRightClickRepulsion` is set.

### Verified equivalence with the original

`examples/compare` contains a harness that runs this port and the original JS library side by side on identical data with identical configuration (see its `compare-go.html` / `compare-js.html` and the deterministic `?sim=0` / `?det=1&alpha0=1` modes). Measured results against `@cosmos.gl/graph` 2.6.3:

- Point positions after rescaling: **bit-identical** (max difference 0.0)
- Fit-view zoom level: **bit-identical** (matches to the last float digit)
- Rendered output: geometrically identical (bounding boxes equal, centroid within 0.06 px; remaining pixel differences are antialiasing noise)
- Physics: a full deterministic simulation step (gravity, many-body, springs, integration) produces **bit-identical positions**; over many steps the two runs diverge *less* than the original diverges from itself with a different random seed (the divergence is chaotic jitter-seed sensitivity, not different physics)
- Frame times: render-only both lock at 60 fps (p95 16.8 ms); with the simulation running, frame times match the original within noise (the simulation is GPU-bound)

### Differences from cosmos.gl

- `Config` is a plain struct created by `NewConfig()`; instead of `setConfig` diffing, mutate fields directly — simulation and interaction parameters are read live every frame, while data-derived defaults are re-applied by the corresponding setter or `Render()`
- "Unset" optional values use Go sentinels: `-1` for indices and `PointGreyoutOpacity`, `""` for optional colors, `-1` in `SetPointClusters`, negative coordinates in `SetClusterPositions`
- `Attribution` renders as plain text (the original sanitizes and injects HTML)
- The FPS monitor is a small built-in overlay instead of the `gl-bench` dependency
- The random jitter PRNG is deterministic per `RandomSeed` but produces a different sequence than the JS `random` package, so layouts are reproducible within cosmos-go but not pixel-identical to cosmos.js runs

### Known Issues

Inherited from the original: starting from version 15.4, iOS has stopped supporting the key WebGL extension powering the Many-Body force implementation (`EXT_float_blend`).

### History

The initial release of cosmos-go (tag `v1-port`) was a port of @cosmograph/cosmos 1.6.1, which is licensed CC-BY-NC-4.0 (non-commercial). The current version is a re-port of the MIT-licensed cosmos.gl 2.6.3 line and carries the MIT license. A port of the cosmos.gl 3.x line (which moved from regl/WebGL1 to luma.gl/WebGL2) may follow as future work.

### License

MIT, same as the ported cosmos.gl 2.6.3 source (see LICENCE).

cosmos-go is derived from [cosmos.gl](https://github.com/cosmograph-org/cosmos) — © Contributors to the cosmos.gl project, created by the [Cosmograph](https://cosmograph.app) team.
## Dependency Graph

Made with [goda](https://github.com/loov/goda):

```
# GOOS=js: the import edges of a wasm program live in js/wasm-tagged
# files and are invisible to a host-context run
GOOS=js GOARCH=wasm go run github.com/loov/goda@latest graph github.com/0magnet/cosmos-go/... | dot -Tsvg -o docs/cosmos-go-goda-graph.svg
```

![Dependency Graph](docs/cosmos-go-goda-graph.svg "github.com/0magnet/cosmos-go Dependency Graph")

## Lines of Code

Made with [gocloc](https://github.com/hhatto/gocloc) (excludes `vendor/`, `node_modules/`, `.git/`):

```
gocloc --not-match-d='(vendor|node_modules|\.git)' .
```

```
-------------------------------------------------------------------------------
Language                     files          blank        comment           code
-------------------------------------------------------------------------------
Go                              21            888            533           6271
JavaScript                       2            117             82            935
HTML                             3              5              0            132
Markdown                         1             42              0            104
YAML                             1              0              9             69
JSON                             3              0              0             29
Bourne Shell                     1              2              3             12
-------------------------------------------------------------------------------
TOTAL                           32           1054            627           7552
-------------------------------------------------------------------------------
```
