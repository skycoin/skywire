import { Injectable } from '@angular/core';
import { ReplaySubject, Observable } from 'rxjs';

// Names for saving the data in localStorage.
const KEY_REFRESH_SECONDS = 'refreshSeconds';
const KEY_SAVED_PKS = 'publicKeysData';
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
}

/**
 * Represents a public key with an associated label.
 */
export class LabeledPublicKey {
  /**
   * Public key string.
   */
  publicKey: string;
  /**
   * Label for identifying the public key.
   */
  label: string;
  /**
   * Allows to know for what the public key points to (like a local node or a remote node).
   */
  keyType: PublicKeyTypes;
}

/**
 * List with the types of labeled public keys. It simply allows to know what the public
 * key points to.
 */
export enum PublicKeyTypes {
  LocalNode = 'ln',
  RemoteNode = 'rn',
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
   * Map with the currently saved labeled public keys, accessible via public key.
   */
  private savedLabeledPublicKeys = new Map<string, LabeledPublicKey>();

  constructor() {
    this.storage = localStorage;
    this.currentRefreshTime = parseInt(this.storage.getItem(KEY_REFRESH_SECONDS), 10) || 10;
    this.currentRefreshTimeSubject.next(this.currentRefreshTime);

    // Load the saved local nodes and labeled public keys.
    this.getSavedLocalNodes().forEach(node => this.savedLocalNodes.set(node.publicKey, node));
    this.getSavedLabeledPublicKeys().forEach(key => this.savedLabeledPublicKeys.set(key.publicKey, key));

    // Process any legacy data, if any.
    this.loadLegacyNodeData();

    // Prevent any unexpected data duplication in the local storage.
    const sanitizedLocalNodesList: LocalNodeInfo[] = [];
    this.savedLocalNodes.forEach(val => sanitizedLocalNodesList.push(val));
    const sanitizedLabeledPublicKeysList: LabeledPublicKey[] = [];
    this.savedLabeledPublicKeys.forEach(val => sanitizedLabeledPublicKeysList.push(val));

    this.saveLocalNodes(sanitizedLocalNodesList);
    this.saveLabeledPublicKeys(sanitizedLabeledPublicKeysList);
  }

  /**
   * Checks if there are data saved in the LEGACY_KEY_NODES key of the local storage. If data is
   * found, it is added to the savedLocalNodes and savedLabeledPublicKeys maps and saved in the
   * appropriate local storage keys. After that, the contents saved in the LEGACY_KEY_NODES key
   * are removed.
   */
  private loadLegacyNodeData() {
    const oldSavedLocalNodes: LegacyNodeInfo[] = JSON.parse(this.storage.getItem(LEGACY_KEY_NODES)) || [];

    if (oldSavedLocalNodes.length > 0) {
      // Get the data saved in the new keys.
      const currentLocalNodes = this.getSavedLocalNodes();
      const currentLabeledPublicKeys = this.getSavedLabeledPublicKeys();

      // Add the data to the new arrays and maps.
      oldSavedLocalNodes.forEach(oldNode => {
        currentLocalNodes.push({
          publicKey: oldNode.publicKey,
          hidden: oldNode.deleted,
        });
        this.savedLocalNodes.set(oldNode.publicKey, currentLocalNodes[currentLocalNodes.length - 1]);

        currentLabeledPublicKeys.push({
          publicKey: oldNode.publicKey,
          keyType: PublicKeyTypes.LocalNode,
          label: oldNode.label,
        });
        this.savedLabeledPublicKeys.set(oldNode.publicKey, currentLabeledPublicKeys[currentLabeledPublicKeys.length - 1]);
      });

      // Save the data.
      this.saveLocalNodes(currentLocalNodes);
      this.saveLabeledPublicKeys(currentLabeledPublicKeys);

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
   */
  includeVisibleLocalNodes(nodesPublicKeys: string[]) {
    this.changeLocalNodesHiddenProperty(nodesPublicKeys, false);
  }

  /**
   * Adds the hidden status of a list of local nodes. If a node was not saved before with a
   * call to includeVisibleLocalNodes, it will be saved.
   */
  setLocalNodesAsHidden(nodesPublicKeys: string[]) {
    this.changeLocalNodesHiddenProperty(nodesPublicKeys, true);
  }

  /**
   * Saves a list of nodes in the list of local nodes this app has already seen. Is a node has
   * been already saved, the hidden status will be updated.
   * @param hidden If the nodes will be set as hidden or not.
   */
  private changeLocalNodesHiddenProperty(nodesPublicKeys: string[], hidden: boolean) {
    // Create sets for the requested public keys and the ones that have not been saved in
    // local storage yet.
    const publicKeysSet = new Set<string>();
    const newKeysToSave = new Set<string>();
    nodesPublicKeys.forEach(key => {
      publicKeysSet.add(key);
      newKeysToSave.add(key);
    });

    // If any change was made and the data has to be saved.
    let modificationsMade = false;

    const localNodes = this.getSavedLocalNodes();
    localNodes.forEach(localNode => {
      if (publicKeysSet.has(localNode.publicKey)) {
        // Any key found in the already saved data is removed from the keys to save.
        if (newKeysToSave.has(localNode.publicKey)) {
          newKeysToSave.delete(localNode.publicKey);
        }

        // If the status if different, update it.
        if (localNode.hidden !== hidden) {
          localNode.hidden = hidden;
          modificationsMade = true;

          this.savedLocalNodes.set(localNode.publicKey, localNode);
        }
      }
    });

    // Add any not already saved key.
    newKeysToSave.forEach(pk => {
      modificationsMade = true;

      const newLocalNode = {
        publicKey: pk,
        hidden: hidden,
      };

      localNodes.push(newLocalNode);
      this.savedLocalNodes.set(pk, newLocalNode);
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
   * Saves a local nodes array in the persistent storage. It replaces any previously saved array.
   */
  private saveLocalNodes(nodes: LocalNodeInfo[]) {
    this.storage.setItem(KEY_LOCAL_NODES, JSON.stringify(nodes));
  }

  /**
   * Gets the saved labeled public keys array, directly from the persistent storage, not the
   * cached map.
   */
  private getSavedLabeledPublicKeys(): LabeledPublicKey[] {
    return JSON.parse(this.storage.getItem(KEY_SAVED_PKS)) || [];
  }

  /**
   * Saves a labeled public keys array in the persistent storage. It replaces any previously
   * saved array.
   */
  private saveLabeledPublicKeys(keys: LabeledPublicKey[]) {
    this.storage.setItem(KEY_SAVED_PKS, JSON.stringify(keys));
  }

  /**
   * Assigns a label to a public key. If the label is empty, the public key is removed from
   * the saved labeled public keys list.
   */
  setLabeledPublicKeyLabel(publicKey: string, label: string, keyType: PublicKeyTypes): void {
    if (!label) {
      // Remove the public key from the cached map.
      if (this.savedLabeledPublicKeys.has(publicKey)) {
        this.savedLabeledPublicKeys.delete(publicKey);
      }

      // Remove the public key from the saved list.
      let previouslySaved = false;
      const keys = this.getSavedLabeledPublicKeys().filter(key => {
        if (key.publicKey === publicKey) {
          previouslySaved = true;

          return false;
        }

        return true;
      });

      // Save the changes.
      if (previouslySaved) {
        this.saveLabeledPublicKeys(keys);
      }
    } else {
      // Get the saved data and update the label.
      let previouslySaved = false;
      const keys = this.getSavedLabeledPublicKeys().map(key => {
        if (key.publicKey === publicKey && key.keyType === keyType) {
          previouslySaved = true;
          key.label = label;

          this.savedLabeledPublicKeys.set(key.publicKey, {
            label: key.label,
            publicKey: key.publicKey,
            keyType: key.keyType,
          });
        }

        return key;
      });

      // If the public keys was not in the saved data, save it.
      if (!previouslySaved) {
        const newPkInfo = {
          label: label,
          publicKey: publicKey,
          keyType: keyType,
        };

        keys.push(newPkInfo);
        this.savedLabeledPublicKeys.set(publicKey, newPkInfo);
        this.saveLabeledPublicKeys(keys);
      } else {
        // Save the updated data.
        this.saveLabeledPublicKeys(keys);
      }
    }
  }

  /**
   * Returns the default label for a public key.
   */
  getDefaultLabel(publicKey: string): string {
    return publicKey.substr(0, 8);
  }

  /**
   * Gets the label data assigned to a public key. If no label has been Assigned to the public
   * key, null is returned.
   */
  getLabeledPublicKey(publicKey: string): LabeledPublicKey {
    if (this.savedLabeledPublicKeys.has(publicKey)) {
      return this.savedLabeledPublicKeys.get(publicKey);
    }

    return null;
  }
}
