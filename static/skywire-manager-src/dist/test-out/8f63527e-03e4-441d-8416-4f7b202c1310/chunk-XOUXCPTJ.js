import {
  Injectable,
  ReplaySubject
} from "./chunk-ZNLGPAUZ.js";

// src/app/services/storage.service.ts
var __decorate = function(decorators, target, key, desc) {
  var c = arguments.length, r = c < 3 ? target : desc === null ? desc = Object.getOwnPropertyDescriptor(target, key) : desc, d;
  if (typeof Reflect === "object" && typeof Reflect.decorate === "function") r = Reflect.decorate(decorators, target, key, desc);
  else for (var i = decorators.length - 1; i >= 0; i--) if (d = decorators[i]) r = (c < 3 ? d(r) : c > 3 ? d(target, key, r) : d(target, key)) || r;
  return c > 3 && r && Object.defineProperty(target, key, r), r;
};
var KEY_REFRESH_SECONDS = "refreshSeconds";
var KEY_SAVED_LABELS = "labelsData";
var KEY_LOCAL_NODES = "localNodesData";
var LEGACY_KEY_NODES = "nodesData";
var LabelInfo = class {
};
var LabeledElementTypes;
(function(LabeledElementTypes2) {
  LabeledElementTypes2["Node"] = "nd";
  LabeledElementTypes2["Transport"] = "tp";
  LabeledElementTypes2["DmsgServer"] = "ds";
})(LabeledElementTypes || (LabeledElementTypes = {}));
var StorageService = class StorageService2 {
  constructor() {
    this.currentRefreshTimeSubject = new ReplaySubject(1);
    this.savedLocalNodes = /* @__PURE__ */ new Map();
    this.savedLabels = /* @__PURE__ */ new Map();
    this.savedVisibleLocalNodes = /* @__PURE__ */ new Set();
  }
  /**
   * Returns the PK of the hypervisor this UI is hosted on. Set by
   * initialize() during app startup from /api/about. Components
   * comparing a visor row's connected-hypervisor list against the
   * local hypervisor (e.g. to render a ★ next to it) use this
   * getter so they don't have to round-trip /api/about themselves.
   */
  getLocalHypervisorPk() {
    return this.hypervisorPk;
  }
  initialize(hypervisorPk) {
    this.storage = localStorage;
    this.hypervisorPk = hypervisorPk;
    this.migrateDataToHvStorage();
    this.currentRefreshTime = parseInt(this.getDataForHv(KEY_REFRESH_SECONDS) || "", 10) || 10;
    this.currentRefreshTimeSubject.next(this.currentRefreshTime);
    this.getSavedLocalNodes().forEach((node) => {
      this.savedLocalNodes.set(node.publicKey, node);
      if (!node.hidden) {
        this.savedVisibleLocalNodes.add(node.publicKey);
      }
    });
    this.getSavedLabels().forEach((label) => this.savedLabels.set(label.id, label));
    this.loadLegacyNodeData();
    const sanitizedLocalNodesList = [];
    this.savedLocalNodes.forEach((val) => sanitizedLocalNodesList.push(val));
    const sanitizedLabelList = [];
    this.savedLabels.forEach((val) => sanitizedLabelList.push(val));
    this.saveLocalNodes(sanitizedLocalNodesList);
    this.saveLabels(sanitizedLabelList);
  }
  /**
   * Gets data in LocalStorage, associated with the current hypervisor.
   * @param key Key of the data to retrieve.
   */
  getDataForHv(key) {
    return this.storage.getItem(this.hypervisorPk + key);
  }
  /**
   * Sets data in LocalStorage, associated with the current hypervisor.
   * @param key Key of the data to save.
   * @param value Data to save
   */
  setDataForHv(key, value) {
    return this.storage.setItem(this.hypervisorPk + key, value);
  }
  /**
   * It checks if there is data saved from a previous version of the app, without being assigned to a
   * hypervisor. If the functions finds old data, it is migrated to the new format and assigned to the
   * current hypervisor.
   */
  migrateDataToHvStorage() {
    const oldSavedRefreshTime = this.storage.getItem(KEY_REFRESH_SECONDS);
    if (oldSavedRefreshTime) {
      const v = parseInt(oldSavedRefreshTime, 10) || 10;
      this.setRefreshTime(v);
      this.storage.removeItem(KEY_REFRESH_SECONDS);
    }
    const oldSavedLocalNodes = this.storage.getItem(KEY_LOCAL_NODES);
    if (oldSavedLocalNodes) {
      const v = JSON.parse(oldSavedLocalNodes) || [];
      this.saveLocalNodes(v);
      this.storage.removeItem(KEY_LOCAL_NODES);
    }
    const oldSavedLabels = this.storage.getItem(KEY_SAVED_LABELS);
    if (oldSavedLabels) {
      const v = JSON.parse(oldSavedLabels) || [];
      this.saveLabels(v);
      this.storage.removeItem(KEY_SAVED_LABELS);
    }
  }
  /**
   * Checks if there are data saved in the LEGACY_KEY_NODES key of the local storage. If data is
   * found, it is added to the savedLocalNodes and savedLabels maps and saved in the
   * appropriate local storage keys. After that, the contents saved in the LEGACY_KEY_NODES key
   * are removed.
   */
  loadLegacyNodeData() {
    const legacy = this.storage.getItem(LEGACY_KEY_NODES);
    const oldSavedLocalNodes = legacy ? JSON.parse(legacy) : [];
    if (oldSavedLocalNodes.length > 0) {
      const currentLocalNodes = this.getSavedLocalNodes();
      const currentLabels = this.getSavedLabels();
      oldSavedLocalNodes.forEach((oldNode) => {
        currentLocalNodes.push({
          publicKey: oldNode.publicKey,
          hidden: oldNode.deleted,
          ip: null
        });
        this.savedLocalNodes.set(oldNode.publicKey, currentLocalNodes[currentLocalNodes.length - 1]);
        if (!oldNode.deleted) {
          this.savedVisibleLocalNodes.add(oldNode.publicKey);
        }
        currentLabels.push({
          id: oldNode.publicKey,
          identifiedElementType: LabeledElementTypes.Node,
          label: oldNode.label
        });
        this.savedLabels.set(oldNode.publicKey, currentLabels[currentLabels.length - 1]);
      });
      this.saveLocalNodes(currentLocalNodes);
      this.saveLabels(currentLabels);
      this.storage.removeItem(LEGACY_KEY_NODES);
    }
  }
  /**
   * Update the time interval (seconds) in which the UI should automatically referesh the data
   * from the backend.
   */
  setRefreshTime(seconds) {
    this.setDataForHv(KEY_REFRESH_SECONDS, seconds.toString());
    this.currentRefreshTime = seconds;
    this.currentRefreshTimeSubject.next(this.currentRefreshTime);
  }
  /**
   * Gets the time interval (seconds) in which the UI should automatically referesh the data
   * from the backend.
   */
  getRefreshTimeObservable() {
    return this.currentRefreshTimeSubject.asObservable();
  }
  /**
   * Gets the time interval (seconds) in which the UI should automatically referesh the data
   * from the backend.
   */
  getRefreshTime() {
    return this.currentRefreshTime;
  }
  /**
   * Whether to show the per-visor switcher as a full second row of tabs (true) or
   * fold it into a dropdown to keep the nav compact (false, the default).
   */
  getShowVisorSwitcherRow() {
    return this.storage.getItem("showVisorSwitcherRow") === "true";
  }
  setShowVisorSwitcherRow(show) {
    this.storage.setItem("showVisorSwitcherRow", show ? "true" : "false");
  }
  /**
   * Saves a list of online nodes in the list of local nodes this app has already seen. If a
   * node was already saved in the list and set as hidden, its hidden status will be removed.
   * @param nodesPublicKeys Public keys of the nodes to save.
   * @param nodesIps Ips of the nodes, if known. Must have the same size as nodesPublicKeys.
   */
  includeVisibleLocalNodes(nodesPublicKeys, nodesIps) {
    this.changeLocalNodesHiddenProperty(nodesPublicKeys, nodesIps, false);
  }
  /**
   * Adds the hidden status of a list of local nodes. If a node was not saved before with a
   * call to includeVisibleLocalNodes, it will be saved.
   * @param nodesPublicKeys Public keys of the nodes to set as hidden.
   * @param nodesIps Ips of the nodes, if known. Must have the same size as nodesPublicKeys.
   */
  setLocalNodesAsHidden(nodesPublicKeys, nodesIps) {
    this.changeLocalNodesHiddenProperty(nodesPublicKeys, nodesIps, true);
  }
  /**
   * Saves a list of nodes in the list of local nodes this app has already seen. Is a node has
   * been already saved, the hidden status will be updated.
   * @param nodesIps Ips of the nodes, if known. Must have the same size as nodesPublicKeys.
   * @param hidden If the nodes will be set as hidden or not.
   */
  changeLocalNodesHiddenProperty(nodesPublicKeys, nodesIps, hidden) {
    if (nodesPublicKeys.length !== nodesIps.length) {
      throw new Error("Invalid params");
    }
    const publicKeysMap = /* @__PURE__ */ new Map();
    const newKeysToSave = /* @__PURE__ */ new Map();
    nodesPublicKeys.forEach((key, i) => {
      publicKeysMap.set(key, nodesIps[i]);
      newKeysToSave.set(key, nodesIps[i]);
    });
    let modificationsMade = false;
    const localNodes = this.getSavedLocalNodes();
    localNodes.forEach((localNode) => {
      if (publicKeysMap.has(localNode.publicKey)) {
        if (newKeysToSave.has(localNode.publicKey)) {
          newKeysToSave.delete(localNode.publicKey);
        }
        if (localNode.ip !== publicKeysMap.get(localNode.publicKey)) {
          localNode.ip = publicKeysMap.get(localNode.publicKey) ?? null;
          modificationsMade = true;
          this.savedLocalNodes.set(localNode.publicKey, localNode);
        }
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
    newKeysToSave.forEach((ip, pk) => {
      modificationsMade = true;
      const newLocalNode = {
        publicKey: pk,
        hidden,
        ip
      };
      localNodes.push(newLocalNode);
      this.savedLocalNodes.set(pk, newLocalNode);
      if (hidden) {
        this.savedVisibleLocalNodes.delete(pk);
      } else {
        this.savedVisibleLocalNodes.add(pk);
      }
    });
    if (modificationsMade) {
      this.saveLocalNodes(localNodes);
    }
  }
  /**
   * Gets the saved local nodes array, directly from the persistent storage, not the cached map.
   */
  getSavedLocalNodes() {
    const stored = this.getDataForHv(KEY_LOCAL_NODES);
    return stored ? JSON.parse(stored) : [];
  }
  /**
   * Gets the saved visible local nodes set, from the cached map. The returned set must not
   * be modiffied.
   */
  getSavedVisibleLocalNodes() {
    return this.savedVisibleLocalNodes;
  }
  /**
   * Saves a local nodes array in the persistent storage. It replaces any previously saved array.
   */
  saveLocalNodes(nodes) {
    this.setDataForHv(KEY_LOCAL_NODES, JSON.stringify(nodes));
  }
  /**
   * Gets the saved labels array, directly from the persistent storage, not the cached map.
   */
  getSavedLabels() {
    const stored = this.getDataForHv(KEY_SAVED_LABELS);
    return stored ? JSON.parse(stored) : [];
  }
  /**
   * Saves a labels array in the persistent storage. It replaces any previously saved array.
   */
  saveLabels(labels) {
    this.setDataForHv(KEY_SAVED_LABELS, JSON.stringify(labels));
  }
  /**
   * Saves a label to identify an element via its id. If the provided label is empty, the id
   * is removed from the saved labels list, if it was saved before.
   */
  saveLabel(id, label, elementType) {
    if (!label) {
      if (this.savedLabels.has(id)) {
        this.savedLabels.delete(id);
      }
      let previouslySaved = false;
      const labels = this.getSavedLabels().filter((currentLabel) => {
        if (currentLabel.id === id) {
          previouslySaved = true;
          return false;
        }
        return true;
      });
      if (previouslySaved) {
        this.saveLabels(labels);
      }
    } else {
      let previouslySaved = false;
      const labels = this.getSavedLabels().map((currentLabel) => {
        if (currentLabel.id === id && currentLabel.identifiedElementType === elementType) {
          previouslySaved = true;
          currentLabel.label = label;
          this.savedLabels.set(currentLabel.id, {
            label: currentLabel.label,
            id: currentLabel.id,
            identifiedElementType: currentLabel.identifiedElementType
          });
        }
        return currentLabel;
      });
      if (!previouslySaved) {
        const newPkInfo = {
          label,
          id,
          identifiedElementType: elementType
        };
        labels.push(newPkInfo);
        this.savedLabels.set(id, newPkInfo);
        this.saveLabels(labels);
      } else {
        this.saveLabels(labels);
      }
    }
  }
  /**
   * Returns the default label for a node.
   *
   * Priority: hostname > local IP > public key. The hostname is the
   * most operator-readable identifier when no explicit label has
   * been set — most deployments name their boxes meaningfully
   * (`raspi-3`, `home-server`, ...), so falling back to that beats
   * showing a `192.168.x.y` LAN IP or the raw 66-char PK. Empty
   * hostname (visor running in a container/sandbox without
   * os.Hostname() support, or older visor binary without the
   * Overview.Hostname field) falls through to the IP path
   * unchanged.
   */
  getDefaultLabel(node) {
    if (!node) {
      return "";
    }
    if (node.hostname) {
      return node.hostname;
    }
    if (node.ip) {
      return node.ip;
    }
    return node.localPk;
  }
  /**
   * Gets the label info assigned to an id. If no label has been assigned to the id, null
   * is returned.
   */
  getLabelInfo(id) {
    if (id && this.savedLabels.has(id)) {
      return this.savedLabels.get(id);
    }
    return null;
  }
};
StorageService = __decorate([
  Injectable({
    providedIn: "root"
  })
], StorageService);

export {
  LabelInfo,
  LabeledElementTypes,
  StorageService
};
//# debugId=2f3218bf-8f0f-5db3-b6d1-c737f91ad553
//# sourceMappingURL=chunk-XOUXCPTJ.js.map
