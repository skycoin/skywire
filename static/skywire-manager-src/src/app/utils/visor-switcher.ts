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
 *   line 1: the node's label (labels auto-populate, so this is usually set;
 *           falls back to the abbreviated PK if somehow empty);
 *   line 2: the full public key, in a slightly smaller font.
 * Icon distinguishes hypervisor / wasm(browser) / regular visors.
 * Shared by the node-list and node (detail) pages so the row is identical on both.
 */
export function visorSwitcherChips(nodes: Node[]): TabButtonData[] {
  return (nodes || []).map(n => {
    return {
      icon: (n as any).isHypervisor ? 'router' : ((n as any).arch === 'wasm' ? 'public' : 'devices'),
      label: n.label || abbreviatePk(n.localPk),
      sublabel: n.localPk || '',
      linkParts: ['/nodes', n.localPk, 'info'],
    } as TabButtonData;
  });
}
