import { Injectable } from '@angular/core';

const KEY_REFRESH_SECONDS = 'KEY_REFRESH_SECONDS';
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

  constructor() {
    this.storage = localStorage;
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

  getNodeLabel(nodeKey: string): string {
    return this.storage.getItem(StorageService.nodeLabelNamespace(nodeKey));
  }

  setRefreshTime(seconds: number) {
    this.storage.setItem(KEY_REFRESH_SECONDS, seconds.toString());
  }

  getRefreshTime(): number {
    return parseInt(this.storage.getItem(KEY_REFRESH_SECONDS), 10) || 5;
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

    this.setNodes(Array.from(nodes));
  }

  removeNode(nodeKey: string) {
    this.setNodes(this.getNodes().filter(n => n.publicKey !== nodeKey));
  }

  getNodes(): NodeInfo[] {
    return JSON.parse(this.storage.getItem(KEY_NODES)) || [];
  }

  private setNodes(nodes: NodeInfo[]) {
    this.storage.setItem(KEY_NODES, JSON.stringify(nodes));
  }
}
