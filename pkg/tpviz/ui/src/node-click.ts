// Shared node-click dispatch used by both graph renderers (vis-network Flat and
// cosmos WebGL). It replicates the pick-mode behavior of the flat view's click
// handler — when a "pick" mode is armed (transport-setup target/remote, group
// pick, local transport, multihop hop, dmsg-health, ping), clicking a node fills
// the corresponding field instead of opening the node-info panel.
//
// The vis-network handler additionally guards on isCluster(); cosmos has no
// clusters, so this trimmed version is safe for it. Kept separate so the two
// renderers can't diverge on pick-mode semantics.

import * as S from './state';
import { showNodeInfo } from './node-info';
import { tpsUpdateInfo, tpsPopulateGroupSelect, mhAddHop } from './tps';

export function handleGraphNodeClick(nodeId: string): void {
  if (S.tpsPickMode) {
    if (S.tpsPickMode === 'target') {
      (document.getElementById('tps-target-pk') as HTMLInputElement).value = nodeId;
    } else if (S.tpsPickMode === 'remote') {
      (document.getElementById('tps-remote-pk') as HTMLInputElement).value = nodeId;
    }
    S.setTpsPickMode(null);
    document.body.style.cursor = 'default';
    tpsUpdateInfo();
    return;
  }
  if (S.tpsGroupPickMode) {
    const mode = S.tpsGroupPickMode === 'target'
      ? (document.getElementById('tps-target-mode') as HTMLSelectElement).value
      : (document.getElementById('tps-remote-mode') as HTMLSelectElement).value;
    let groupValue: any = null;
    if (mode === 'ip' && S.ipGroupsData && S.ipGroupsData.groups) {
      groupValue = S.ipGroupsData.groups[nodeId];
    } else if (mode === 'country' && S.visorServices[nodeId]) {
      groupValue = S.visorServices[nodeId].country;
    }
    if (groupValue !== null && groupValue !== undefined) {
      const selectEl = document.getElementById(
        S.tpsGroupPickMode === 'target' ? 'tps-target-group' : 'tps-remote-group') as HTMLSelectElement;
      tpsPopulateGroupSelect(selectEl, mode);
      selectEl.value = String(groupValue);
      tpsUpdateInfo();
    }
    S.setTpsGroupPickMode(null);
    document.body.style.cursor = 'default';
    return;
  }
  if (S.localTpPickMode) {
    const input = document.getElementById('local-tp-remote-pk') as HTMLInputElement;
    if (input) { input.value = nodeId; }
    S.setLocalTpPickMode(false);
    document.body.style.cursor = 'default';
    return;
  }
  if (S.mhPickMode) {
    mhAddHop(nodeId);
    S.setMhPickMode(false);
    document.body.style.cursor = 'default';
    return;
  }
  if (S.dmsgHealthPickMode) {
    (document.getElementById('dmsg-health-pk') as HTMLInputElement).value = nodeId;
    S.setDmsgHealthPickMode(false);
    document.body.style.cursor = 'default';
    return;
  }
  if (S.pingPickMode) {
    const pk = nodeId.startsWith('dmsg-srv-') ? nodeId.substring(9) : nodeId;
    (document.getElementById('ping-pk') as HTMLInputElement).value = pk;
    S.setPingPickMode(false);
    document.body.style.cursor = 'default';
    return;
  }
  showNodeInfo(nodeId);
}
