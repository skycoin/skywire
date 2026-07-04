import { Node } from '../app.datatypes';
import { TabButtonData } from '../components/layout/top-bar/top-bar.component';

/**
 * Formats a public key as the first 5 and last 5 characters joined by "..." —
 * used as the visor-switcher chip label when a node has no custom label.
 */
export function abbreviatePk(pk: string): string {
  if (!pk) { return '?'; }
  return pk.length <= 12 ? pk : pk.substring(0, 5) + '...' + pk.substring(pk.length - 5);
}

/**
 * Builds the top-bar visor-switcher chips — one per visor in the hypervisor's
 * list. Each chip is two lines:
 *   line 1: the node's label, or its abbreviated PK (first5...last5) if unlabeled;
 *   line 2: public-ip / lan-ip (both when known and distinct, else whichever
 *           is available).
 * Icon distinguishes hypervisor / wasm(browser) / regular visors.
 * Shared by the node-list and node (detail) pages so the row is identical on both.
 */
export function visorSwitcherChips(nodes: Node[]): TabButtonData[] {
  return (nodes || []).map(n => {
    const pub = (n.publicIp || '').trim();
    const lan = (n.ip || '').trim();
    let sub = '';
    if (pub && lan && pub !== lan) { sub = pub + ' / ' + lan; } else { sub = pub || lan || ''; }
    return {
      icon: (n as any).isHypervisor ? 'router' : ((n as any).arch === 'wasm' ? 'public' : 'devices'),
      label: n.label || abbreviatePk(n.localPk),
      sublabel: sub,
      linkParts: ['/nodes', n.localPk, 'info'],
    } as TabButtonData;
  });
}
