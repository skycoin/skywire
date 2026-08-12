// Dispatcher for the two WebGL views.
//
// tpviz currently ships the graph engine twice: the JavaScript one
// (cosmos-graph.ts, @cosmograph/cosmos 1.6.1) and the Go/wasm one
// (cosmos-go-graph.ts, a port of cosmos.gl 2.6.3), so the second can be
// compared against the first on the same data before the first is retired.
//
// Everything outside those two modules asks here rather than naming an engine,
// so exactly one view is ever driven and adding — or, later, removing — an
// implementation touches this file instead of a dozen call sites.

import {
  isCosmosActive, cosmosFit, cosmosZoomBy, cosmosFocusNode, cosmosSetPhysics, updateCosmosData,
} from './cosmos-graph';
import {
  isCosmosGoActive, cosmosGoFit, cosmosGoZoomBy, cosmosGoFocusNode, cosmosGoSetPhysics, updateCosmosGoData,
} from './cosmos-go-graph';

/** webglActive reports whether either WebGL view is showing. */
export function webglActive(): boolean {
  return isCosmosActive() || isCosmosGoActive();
}

/** webglIsGo reports which of the two is showing, for comparison tooling. */
export function webglIsGo(): boolean { return isCosmosGoActive(); }

export function webglUpdateData(): void {
  if (isCosmosGoActive()) { updateCosmosGoData(); return; }
  if (isCosmosActive()) { updateCosmosData(); }
}

export function webglFit(): void {
  if (isCosmosGoActive()) { cosmosGoFit(); return; }
  if (isCosmosActive()) { cosmosFit(); }
}

export function webglZoomBy(factor: number): void {
  if (isCosmosGoActive()) { cosmosGoZoomBy(factor); return; }
  if (isCosmosActive()) { cosmosZoomBy(factor); }
}

export function webglFocusNode(pk: string): void {
  if (isCosmosGoActive()) { cosmosGoFocusNode(pk); return; }
  if (isCosmosActive()) { cosmosFocusNode(pk); }
}

export function webglSetPhysics(on: boolean): void {
  if (isCosmosGoActive()) { cosmosGoSetPhysics(on); return; }
  if (isCosmosActive()) { cosmosSetPhysics(on); }
}
