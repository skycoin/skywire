# spheregraph

Embeds a weighted graph on the unit sphere so that **great-circle distance
approximates the measured weight**, then tessellates the result with a
spherical Delaunay triangulation and its dual Voronoi diagram.

The motivating case is a latency matrix: given round-trip times between hosts,
place every host on a globe so that "far apart on screen" means "slow between
them". The weights are named in milliseconds because that is what they were,
but any positive dissimilarity works.

## Why not a force layout

A force simulation with uniform springs ignores the weights entirely — it
produces an aesthetically even graph, not a metric one. And a GPU force-graph
renderer typically has one global link-distance scalar, so it cannot express
per-edge distance at all. The positions have to be solved first and handed to
the renderer.

The solver is **stress majorization** (Gansner, Koren and North, *Graph Drawing
by Stress Majorization*, GD 2004), adapted to the sphere: each iteration moves a
point toward the weighted mean of where its neighbours would place it, then
renormalizes onto the sphere. On a sparse graph this converges in tens of
iterations and each one is O(edges) — 43k edges over 1.3k points is a few
million operations.

## Install

```
go get github.com/0magnet/spheregraph
```

## Use

```go
edges := []spheregraph.Edge{
    {A: 0, B: 1, MS: 12},
    {A: 1, B: 2, MS: 80},
    {A: 0, B: 2, MS: 95},
}
pts := spheregraph.Embed(3, edges, spheregraph.DefaultParams())

cells := spheregraph.Voronoi(spheregraph.SeparateCoincident(pts))
for _, c := range cells {
    fmt.Println(c.Site, len(c.Polygon))
}
```

### Embedding

- `Embed(n int, edges []Edge, p Params) []Vec3` — returns one unit vector per
  index `0..n-1`. Points with no measured edge are kept in place rather than
  dropped, so indices stay aligned with the caller's own slice.
- `Params.Seed` makes the layout deterministic: the same input embeds the same
  way on every machine and across refreshes, which matters if several observers
  should agree on the picture.
- `Params.FloorMS` / `CeilMS` clamp the weight range so one pathological
  measurement cannot dominate the whole layout.
- `Stress(pts, edges, p) float64` scores a layout; `Angle(a, b)` is the
  great-circle distance between two points.

### Tessellation

- `Delaunay(pts []Vec3) []Triangle` — spherical Delaunay via the convex hull.
- `Voronoi(pts []Vec3) []Cell` — the dual. Each `Cell` is a site index and a
  closed spherical polygon.
- `SeparateCoincident(pts []Vec3) []Vec3` — nudges exactly-coincident sites
  apart. Two hosts on one machine legitimately embed to the same point, which is
  correct but degenerate for hull construction and invisible when drawn.

## Notes

Standard library only.

Extracted from [skywire](https://github.com/skycoin/skywire), where it lays out
a network of hosts by measured RTT.
