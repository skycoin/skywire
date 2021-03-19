import { Injectable } from '@angular/core';
import { ReplaySubject, Observable } from 'rxjs';

import { Node } from '../app.datatypes';

// Names for saving the data in localStorage.
const KEY_REFRESH_SECONDS = 'refreshSeconds';
const KEY_SAVED_LABELS = 'labelsData';
const KEY_LOCAL_NODES = 'localNodesData';

/**
 * Key used in the previous version for saving data about the local nodes. The same data is
 * now saved using other keys, but the key remains here to be able to migrate the data.
 */
const LEGACY_KEY_NODES = 'nodesData';

/**
 * Class used in the previous version to Represent a node saved in persistent storage. The
 * class remains here to be able to migrate the data.
 */
export class LegacyNodeInfo {
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
 * Represents a saved local node. This allows to "remember" the nodes that have been seen
 * before and which ones should not be shown anymore as offline in the node list.
 */
export class LocalNodeInfo {
  /**
   * Public key of the node.
   */
  publicKey: string;
  /**
   * If true, the node should not be shown in the node list if it is offline.
   */
  hidden: boolean;
  /**
   * IP the node had the last time it was seen.
   */
  ip: string;
}

/**
 * Represents a label to identify an element.
 */
export class LabelInfo {
  /**
   * ID of the element.
   */
  id: string;
  /**
   * Label for identifying the element.
   */
  label: string;
  /**
   * Allows to know what the element is (like a node or a dmsg server).
   */
  identifiedElementType: LabeledElementTypes;
}

/**
 * List with the types of labeled elements.
 */
export enum LabeledElementTypes {
  Node = 'nd',
  Transport = 'tp',
  DmsgServer = 'ds',
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
   * Map with the currently saved local nodes, accessible via public key.
   */
  private savedLocalNodes = new Map<string, LocalNodeInfo>();
  /**
   * Map with the currently saved labels, accessible via id.
   */
  private savedLabels = new Map<string, LabelInfo>();
  /**
   * Set with the public keys of the currently saved local nodes which have not been set a hidden.
   */
  private savedVisibleLocalNodes = new Set<string>();

  constructor() {
    this.storage = localStorage;
    this.currentRefreshTime = parseInt(this.storage.getItem(KEY_REFRESH_SECONDS), 10) || 10;
    this.currentRefreshTimeSubject.next(this.currentRefreshTime);

    // Load the saved local nodes and labels.
    this.getSavedLocalNodes().forEach(node => {
      this.savedLocalNodes.set(node.publicKey, node);
      if (!node.hidden) {
        this.savedVisibleLocalNodes.add(node.publicKey);
      }
    });
    this.getSavedLabels().forEach(label => this.savedLabels.set(label.id, label));

    // Process any legacy data, if any.
    this.loadLegacyNodeData();

    // Prevent any unexpected data duplication in the local storage.
    const sanitizedLocalNodesList: LocalNodeInfo[] = [];
    this.savedLocalNodes.forEach(val => sanitizedLocalNodesList.push(val));
    const sanitizedLabelList: LabelInfo[] = [];
    this.savedLabels.forEach(val => sanitizedLabelList.push(val));

    this.saveLocalNodes(sanitizedLocalNodesList);
    this.saveLabels(sanitizedLabelList);
  }

  /**
   * Checks if there are data saved in the LEGACY_KEY_NODES key of the local storage. If data is
   * found, it is added to the savedLocalNodes and savedLabels maps and saved in the
   * appropriate local storage keys. After that, the contents saved in the LEGACY_KEY_NODES key
   * are removed.
   */
  private loadLegacyNodeData() {
    const oldSavedLocalNodes: LegacyNodeInfo[] = JSON.parse(this.storage.getItem(LEGACY_KEY_NODES)) || [];

    if (oldSavedLocalNodes.length > 0) {
      // Get the data saved in the new storage keys.
      const currentLocalNodes = this.getSavedLocalNodes();
      const currentLabels = this.getSavedLabels();

      // Add the data to the new arrays and maps.
      oldSavedLocalNodes.forEach(oldNode => {
        currentLocalNodes.push({
          publicKey: oldNode.publicKey,
          hidden: oldNode.deleted,
          ip: null,
        });
        this.savedLocalNodes.set(oldNode.publicKey, currentLocalNodes[currentLocalNodes.length - 1]);
        if (!oldNode.deleted) {
          this.savedVisibleLocalNodes.add(oldNode.publicKey);
        }

        currentLabels.push({
          id: oldNode.publicKey,
          identifiedElementType: LabeledElementTypes.Node,
          label: oldNode.label,
        });
        this.savedLabels.set(oldNode.publicKey, currentLabels[currentLabels.length - 1]);
      });

      // Save the data.
      this.saveLocalNodes(currentLocalNodes);
      this.saveLabels(currentLabels);

      this.storage.removeItem(LEGACY_KEY_NODES);
    }
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
   * Saves a list of online nodes in the list of local nodes this app has already seen. If a
   * node was already saved in the list and set as hidden, its hidden status will be removed.
   * @param nodesPublicKeys Public keys of the nodes to save.
   * @param nodesIps Ips of the nodes, if known. Must have the same size as nodesPublicKeys.
   */
  includeVisibleLocalNodes(nodesPublicKeys: string[], nodesIps: string[]) {
    this.changeLocalNodesHiddenProperty(nodesPublicKeys, nodesIps, false);
  }

  /**
   * Adds the hidden status of a list of local nodes. If a node was not saved before with a
   * call to includeVisibleLocalNodes, it will be saved.
   * @param nodesPublicKeys Public keys of the nodes to set as hidden.
   * @param nodesIps Ips of the nodes, if known. Must have the same size as nodesPublicKeys.
   */
  setLocalNodesAsHidden(nodesPublicKeys: string[], nodesIps: string[]) {
    this.changeLocalNodesHiddenProperty(nodesPublicKeys, nodesIps, true);
  }

  /**
   * Saves a list of nodes in the list of local nodes this app has already seen. Is a node has
   * been already saved, the hidden status will be updated.
   * @param nodesIps Ips of the nodes, if known. Must have the same size as nodesPublicKeys.
   * @param hidden If the nodes will be set as hidden or not.
   */
  private changeLocalNodesHiddenProperty(nodesPublicKeys: string[], nodesIps: string[], hidden: boolean) {
    if (nodesPublicKeys.length !== nodesIps.length) {
      throw new Error('Invalid params');
    }

    // Create maps for the requested public keys and the ones that have not been saved in
    // local storage yet. The pk is used as key and the IP as value.
    const publicKeysMap = new Map<string, string>();
    const newKeysToSave = new Map<string, string>();
    nodesPublicKeys.forEach((key, i) => {
      publicKeysMap.set(key, nodesIps[i]);
      newKeysToSave.set(key, nodesIps[i]);
    });

    // If any change was made and the data has to be saved.
    let modificationsMade = false;

    const localNodes = this.getSavedLocalNodes();
    localNodes.forEach(localNode => {
      if (publicKeysMap.has(localNode.publicKey)) {
        // Any key found in the already saved data is removed from the keys to save.
        if (newKeysToSave.has(localNode.publicKey)) {
          newKeysToSave.delete(localNode.publicKey);
        }

        // If the ip if different, update it.
        if (localNode.ip !== publicKeysMap.get(localNode.publicKey)) {
          localNode.ip = publicKeysMap.get(localNode.publicKey);
          modificationsMade = true;
          this.savedLocalNodes.set(localNode.publicKey, localNode);
        }

        // If the hidden status if different, update it.
        if (localNode.hidden !== hidden) {
          localNode.hidden = hidden;
          modificationsMade = true;

          this.savedLocalNodes.set(localNode.publicKey, localNode);
          if (hidden) {
            this.savedVisibleLocalNodes.delete(localNode.publicKey);
          } else {
            this.savedVisibleLocalNodes.add(localNode.publicKey);
          }
        }
      }
    });

    // Add any not already saved key.
    newKeysToSave.forEach((ip, pk) => {
      modificationsMade = true;

      const newLocalNode = {
        publicKey: pk,
        hidden: hidden,
        ip: ip,
      };

      localNodes.push(newLocalNode);
      this.savedLocalNodes.set(pk, newLocalNode);
      if (hidden) {
        this.savedVisibleLocalNodes.delete(pk);
      } else {
        this.savedVisibleLocalNodes.add(pk);
      }
    });

    // Save the changes, if needed.
    if (modificationsMade) {
      this.saveLocalNodes(localNodes);
    }
  }

  /**
   * Gets the saved local nodes array, directly from the persistent storage, not the cached map.
   */
  getSavedLocalNodes(): LocalNodeInfo[] {
    return JSON.parse(this.storage.getItem(KEY_LOCAL_NODES)) || [];
  }

  /**
   * Gets the saved visible local nodes set, from the cached map. The returned set must not
   * be modiffied.
   */
  getSavedVisibleLocalNodes(): Set<string> {
    return this.savedVisibleLocalNodes;
  }

  /**
   * Saves a local nodes array in the persistent storage. It replaces any previously saved array.
   */
  private saveLocalNodes(nodes: LocalNodeInfo[]) {
    this.storage.setItem(KEY_LOCAL_NODES, JSON.stringify(nodes));
  }

  /**
   * Gets the saved labels array, directly from the persistent storage, not the cached map.
   */
  getSavedLabels(): LabelInfo[] {
    return JSON.parse(this.storage.getItem(KEY_SAVED_LABELS)) || [];
  }

  /**
   * Saves a labels array in the persistent storage. It replaces any previously saved array.
   */
  private saveLabels(labels: LabelInfo[]) {
    this.storage.setItem(KEY_SAVED_LABELS, JSON.stringify(labels));
  }

  /**
   * Saves a label to identify an element via its id. If the provided label is empty, the id
   * is removed from the saved labels list, if it was saved before.
   */
  saveLabel(id: string, label: string, elementType: LabeledElementTypes): void {
    if (!label) {
      // Remove the label from the cached map.
      if (this.savedLabels.has(id)) {
        this.savedLabels.delete(id);
      }

      // Remove the label from the saved list.
      let previouslySaved = false;
      const labels = this.getSavedLabels().filter(currentLabel => {
        if (currentLabel.id === id) {
          previouslySaved = true;

          return false;
        }

        return true;
      });

      // Save the changes.
      if (previouslySaved) {
        this.saveLabels(labels);
      }
    } else {
      // Get the saved data and update the label.
      let previouslySaved = false;
      const labels = this.getSavedLabels().map(currentLabel => {
        if (currentLabel.id === id && currentLabel.identifiedElementType === elementType) {
          previouslySaved = true;
          currentLabel.label = label;

          this.savedLabels.set(currentLabel.id, {
            label: currentLabel.label,
            id: currentLabel.id,
            identifiedElementType: currentLabel.identifiedElementType,
          });
        }

        return currentLabel;
      });

      // If the label was not in the saved data, save it.
      if (!previouslySaved) {
        const newPkInfo: LabelInfo = {
          label: label,
          id: id,
          identifiedElementType: elementType,
        };

        labels.push(newPkInfo);
        this.savedLabels.set(id, newPkInfo);
        this.saveLabels(labels);
      } else {
        // Save the updated data.
        this.saveLabels(labels);
      }
    }
  }

  /**
   * Returns the default label for a node.
   */
  getDefaultLabel(node: Node): string {
    if (!node) {
      return '';
    }

    if (node.ip) {
      return node.ip;
    }

    return node.localPk.substr(0, 8);
  }

  /**
   * Gets the label info assigned to an id. If no label has been assigned to the id, null
   * is returned.
   */
  getLabelInfo(id: string): LabelInfo {
    if (this.savedLabels.has(id)) {
      return this.savedLabels.get(id);
    }

    return null;
  }
}
