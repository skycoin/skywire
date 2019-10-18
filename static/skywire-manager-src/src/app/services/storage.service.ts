import { Injectable } from '@angular/core';

const KEY_REFRESH_SECONDS = 'refreshSeconds';
const KEY_DEFAULT_LANG = 'KEY_DEFAULT_LANG';
const KEY_NODES = 'nodesData';

export class NodeInfo {
  publicKey: string;
  label: string;
}

@Injectable({
  providedIn: 'root'
})
export class StorageService {
  private storage: Storage;
  private currentRefreshTime: number;
  private savedNodes = new Map<string, NodeInfo>();

  constructor() {
    this.storage = localStorage;
    this.currentRefreshTime = parseInt(this.storage.getItem(KEY_REFRESH_SECONDS), 10) || 10;
    this.getNodes().forEach(node => this.savedNodes.set(node.publicKey, node));
  }

  private static nodeLabelNamespace(nodeKey: string): string {
    return `${nodeKey}-label`;
  }

  setNodeLabel(nodeKey: string, nodeLabel: string): void {
    const nodes = this.getNodes().map(node => {
      if (node.publicKey === nodeKey) {
        node.label = nodeLabel;
      }

      return node;
    });

    this.setNodes(nodes);
  }

  setRefreshTime(seconds: number) {
    this.storage.setItem(KEY_REFRESH_SECONDS, seconds.toString());
    this.currentRefreshTime = seconds;
  }

  getRefreshTime(): number {
    return this.currentRefreshTime;
  }

  setDefaultLanguage(lang: string): void {
    this.storage.setItem(KEY_DEFAULT_LANG, lang);
  }

  getDefaultLanguage(): string {
    return this.storage.getItem(KEY_DEFAULT_LANG) || 'en';
  }

  addNode(nodeInfo: NodeInfo) {
    const nodes = this.getNodes();
    nodes.push(nodeInfo);

    this.savedNodes.set(nodeInfo.publicKey, nodeInfo);

    this.setNodes(Array.from(nodes));
  }

  removeNode(nodeKey: string) {
    this.savedNodes.delete(nodeKey);
    this.setNodes(this.getNodes().filter(n => n.publicKey !== nodeKey));
  }

  getNodes(): NodeInfo[] {
    return JSON.parse(this.storage.getItem(KEY_NODES)) || [];
  }

  getNodeLabel(nodeKey: string): string {
    if (this.savedNodes.has(nodeKey)) {
      return this.savedNodes.get(nodeKey).label;
    }

    return '';
  }

  isNodeSaved(nodeKey: string): boolean {
    return this.savedNodes.has(nodeKey);
  }

  private setNodes(nodes: NodeInfo[]) {
    this.storage.setItem(KEY_NODES, JSON.stringify(nodes));
  }
}
