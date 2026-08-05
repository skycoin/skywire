// lifecycle.ts — teardown for the mountable tpviz bundle.
//
// mount()/unmount() (mount.ts) inject and remove the app DOM, but the app also
// spins up a raft of timers, requestAnimationFrame render loops and a WebSocket
// that live on module state, not on the DOM. Before this, unmount() cleared the
// DOM but left every loop running — so switching away from the viz tab (which
// unmounts the bundle) left the apps poll, countdown, local-visor refresh,
// group-force interval, the flow / globe / satellite / cosmos render loops and
// the visor WebSocket all firing forever against a detached document: CPU burn,
// repeated network fetches and console spam while the tab isn't even visible.
//
// teardown() stops them all. It is idempotent and null-safe — safe to call when
// a given loop was never started (each guard checks its handle first).

import * as S from './state';
import { clearFlowAnimation } from './flow-animation';
import { stopGroupBoundaryEnforcement } from './grouping';
import { disposeGlobe } from './globe';
import { cosmosTeardown } from './cosmos-graph';
import { stopAppsRefresh } from './apps';

export function teardown(): void {
  // Interval timers held in module state.
  if (S.countdownInterval) { clearInterval(S.countdownInterval); S.setCountdownInterval(null); }
  if (S.localVisorRefreshInterval) { clearInterval(S.localVisorRefreshInterval); S.setLocalVisorRefreshInterval(null); }
  if (S.highlightedRouteTimeout) { clearTimeout(S.highlightedRouteTimeout); S.setHighlightedRouteTimeout(null); }

  // rAF render loops held in module state (clearFlowAnimation/stopGroupBoundary
  // don't cancel these frames themselves).
  if (S.flowAnimationId != null) { cancelAnimationFrame(S.flowAnimationId); S.setFlowAnimationId(null); }
  if (S.satelliteOrbitAnimId != null) { cancelAnimationFrame(S.satelliteOrbitAnimId); S.setSatelliteOrbitAnimId(null); }

  // Module-owned loops + resources, via each module's own stop helper.
  stopAppsRefresh();               // apps 5s /api/apps poll
  stopGroupBoundaryEnforcement();  // group-force interval + grouping fns
  clearFlowAnimation();            // flow particles + overlay canvas
  disposeGlobe();                  // globe three.js rAF + WebGL renderer
  cosmosTeardown();                // cosmos WebGL render loop + boundary overlay

  // Local-visor WebSocket (kept reconnecting otherwise).
  if (S.localVisorWS) {
    try { S.localVisorWS.close(); } catch { /* noop */ }
    S.setLocalVisorWS(null);
  }
  S.setVisorConnected(false);
}
