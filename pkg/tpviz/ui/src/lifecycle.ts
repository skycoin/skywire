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
import { cosmosTeardown, cosmosPause, cosmosResume } from './cosmos-graph';
import { stopAppsRefresh, initApps } from './apps';
import { startLocalVisorFastRefresh } from './local-visor';
import { updateCountdown } from './api';

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

// pauseWhileHidden suspends the visualizer's ongoing work while it isn't visible
// (backgrounded browser tab, or the mount element off-screen/hidden): it stops
// DATA SYNC (local-visor poll/WebSocket, apps poll, the auto-refresh countdown)
// and RENDERING (sets S.renderPaused so the flow/satellite/globe/boundary rAF
// loops skip their draw; pauses the cosmos WebGL render). Unlike teardown() it
// destroys nothing — resumeWhenVisible() restarts everything. The cosmos force
// sim is meant to keep running while visible, so it's suspended/resumed, not
// rested. Idempotent (S.renderPaused guards re-entry).
export function pauseWhileHidden(): void {
  if (S.renderPaused) { return; }
  S.setRenderPaused(true);

  // Data sync.
  if (S.countdownInterval) { clearInterval(S.countdownInterval); S.setCountdownInterval(null); }
  if (S.localVisorRefreshInterval) { clearInterval(S.localVisorRefreshInterval); S.setLocalVisorRefreshInterval(null); }
  if (S.localVisorWS) { try { S.localVisorWS.close(); } catch { /* noop */ } S.setLocalVisorWS(null); }
  stopAppsRefresh();

  // Rendering (rAF loops self-idle via S.renderPaused; cosmos WebGL here).
  cosmosPause();
}

// resumeWhenVisible restores what pauseWhileHidden suspended: unpauses rendering
// (the rAF loops resume drawing on the next frame; the cosmos sim restarts) and
// re-arms the data-sync loops. The local-visor refresh re-establishes its WS/HTTP
// poll; the countdown re-arms (and triggers a data refresh itself if it lapsed
// while hidden); the apps poll re-inits only if its section is on screen.
export function resumeWhenVisible(): void {
  if (!S.renderPaused) { return; }
  S.setRenderPaused(false);

  cosmosResume();

  startLocalVisorFastRefresh();
  if (!S.countdownInterval) { S.setCountdownInterval(setInterval(updateCountdown, 1000)); }
  const appsSection = document.getElementById('section-apps');
  if (appsSection && appsSection.style.display === 'block') { initApps(); }
}

// installVisibilityGate wires pauseWhileHidden/resumeWhenVisible to the page's
// visibility and returns a teardown fn. Used by BOTH entry paths: the standalone
// /tp-viz/ page (index.ts) and the embedded mount() (Angular tab / WinBox).
//   - document `visibilitychange` covers the whole browser tab being backgrounded.
//   - when `root` is given, an IntersectionObserver also fires the gate when that
//     element itself is off-screen/hidden (a display:none host tab, a covered
//     WinBox window, scrolled away) — cases where the tab is visible so the
//     browser does NOT throttle rAF/timers on its own.
export function installVisibilityGate(root: HTMLElement | null): () => void {
  let tabHidden = document.hidden;
  let offscreen = false;
  const apply = () => { if (tabHidden || offscreen) { pauseWhileHidden(); } else { resumeWhenVisible(); } };
  const onVisibility = () => { tabHidden = document.hidden; apply(); };
  document.addEventListener('visibilitychange', onVisibility);
  let io: IntersectionObserver | null = null;
  if (root && typeof IntersectionObserver !== 'undefined') {
    io = new IntersectionObserver((entries) => {
      offscreen = !entries.some((e) => e.isIntersecting);
      apply();
    }, { threshold: 0 });
    io.observe(root);
  }
  // Apply the initial state (e.g. mounted in an already-backgrounded tab); the
  // IntersectionObserver's first callback refines `offscreen` right after.
  apply();
  return () => {
    document.removeEventListener('visibilitychange', onVisibility);
    if (io) { io.disconnect(); io = null; }
  };
}
