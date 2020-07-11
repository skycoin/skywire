import { Injectable } from '@angular/core';
import { ReplaySubject, Observable } from 'rxjs';

// Names for saving the data in localStorage.
const KEY_REFRESH_SECONDS = 'refreshSeconds';
const KEY_NODES = 'nodesData';

/**
 * Represents a node saved in persistent storage.
 */
export class NodeInfo {
  /**
   * Public key of the node.
   */
  publicKey: string;
  /**
   * Label of the node.
   */
  label: string;
  /**
   * If true, the node should not be shown in the node list.
   */
  deleted: boolean;
}

/**
 * Allows to manage data in persistent storage.
 */
@Injectable({
  providedIn: 'root'
})
export class StorageService {
  private storage: Storage;
  /**
   * The currently saved value of the time interval (seconds) in which the UI should automatically
   * referesh the data from the backend.
   */
  private currentRefreshTime: number;
  private currentRefreshTimeSubject = new ReplaySubject<number>(1);
  /**
   * Map with the currently saved node.
   */
  private savedNodes = new Map<string, NodeInfo>();

  constructor() {
    this.storage = localStorage;
    this.currentRefreshTime = parseInt(this.storage.getItem(KEY_REFRESH_SECONDS), 10) || 10;
    this.currentRefreshTimeSubject.next(this.currentRefreshTime);
    this.getNodes().forEach(node => this.savedNodes.set(node.publicKey, node));
  }

  /**
   * Update the time interval (seconds) in which the UI should automatically referesh the data
   * from the backend.
   */
  setRefreshTime(seconds: number) {
    this.storage.setItem(KEY_REFRESH_SECONDS, seconds.toString());
    this.currentRefreshTime = seconds;
    this.currentRefreshTimeSubject.next(this.currentRefreshTime);
  }

  /**
   * Gets the time interval (seconds) in which the UI should automatically referesh the data
   * from the backend.
   */
  getRefreshTimeObservable(): Observable<number> {
    return this.currentRefreshTimeSubject.asObservable();
  }

  /**
   * Gets the time interval (seconds) in which the UI should automatically referesh the data
   * from the backend.
   */
  getRefreshTime(): number {
    return this.currentRefreshTime;
  }

  /**
   * Saves a node.
   */
  addNode(nodeInfo: NodeInfo) {
    const nodes = this.getNodes();
    nodes.push(nodeInfo);

    this.savedNodes.set(nodeInfo.publicKey, nodeInfo);

    this.setNodes(Array.from(nodes));
  }

  /**
   * Sets a label to a node. If the label is empty, a default label is used.
   * If the node has not ben saved, it is saved.
   */
  setNodeLabel(nodeKey: string, nodeLabel: string): void {
    if (!nodeLabel) {
      nodeLabel = this.getDefaultNodeLabel(nodeKey);
    }

    let saved = false;
    const nodes = this.getNodes().map(node => {
      if (node.publicKey === nodeKey) {
        saved = true;
        node.label = nodeLabel;

        this.savedNodes.set(node.publicKey, {
          label: nodeLabel,
          publicKey: node.publicKey,
          deleted: node.deleted,
        });
      }

      return node;
    });

    if (!saved) {
      this.addNode({
        label: nodeLabel,
        publicKey: nodeKey,
        deleted: false,
      });
    } else {
      // Update the saved nodes array, to save the changes.
      this.setNodes(nodes);
    }
  }

  /**
   * Changes the "deleted" state of a saved node. If the node has not been
   * saved, nothing happens.
   */
  changeNodeState(nodeKey: string, deleted: boolean) {
    if (this.savedNodes.has(nodeKey)) {
      this.savedNodes.get(nodeKey).deleted = deleted;
    }
    this.setNodes(this.getNodes().map(val => {
      if (val.publicKey === nodeKey) {
        val.deleted = deleted;
      }

      return val;
    }));
  }

  /**
   * Gets the saved nodes array, directly from the persistent storage, not the cached map.
   */
  getNodes(): NodeInfo[] {
    return JSON.parse(this.storage.getItem(KEY_NODES)) || [];
  }

  /**
   * Gets the label of a saved node. If the node has not been saved, a default label
   * is returned and the node is saved with it.
   */
  getNodeLabel(nodeKey: string): string {
    if (this.savedNodes.has(nodeKey)) {
      return this.savedNodes.get(nodeKey).label;
    }

    const newLabel = this.getDefaultNodeLabel(nodeKey);
    this.addNode({
      publicKey: nodeKey,
      label: newLabel,
      deleted: false,
    });

    return newLabel;
  }

  /**
   * Returns a default label for a node.
   */
  private getDefaultNodeLabel(nodeKey: string): string {
    return nodeKey.substr(0, 8);
  }

  /**
   * Saves a node array in the persistent storage. It replaces any previously saved
   * node array.
   */
  private setNodes(nodes: NodeInfo[]) {
    this.storage.setItem(KEY_NODES, JSON.stringify(nodes));
  }
}
