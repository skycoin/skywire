import { Injectable } from '@angular/core';
import { HttpErrorResponse } from '@angular/common/http';
import { Observable, Subscription, BehaviorSubject, of } from 'rxjs';
import { flatMap, map, mergeMap, delay, tap } from 'rxjs/operators';
import BigNumber from 'bignumber.js';

import { StorageService } from './storage.service';
import { HealthInfo, Node, Route, Transport } from '../app.datatypes';
import { ApiService } from './api.service';
import { TransportService } from './transport.service';
import { RouteService } from './route.service';
import { processServiceError } from '../utils/errors';
import { OperationError } from '../utils/operation-error';
import { AppConfig } from '../app.config';

/**
 * Response returned by the node and node list observables.
 */
export interface BackendData {
  /**
   * Last obtained node or node list. If the last operation for getting the data ended in an
   * error, this property may still have an previously obtained value.
   */
  data: Node[] | Node;
  /**
   * Error found while trying to get the data. If will only have a value if the last
   * try ended in an error.
   */
  error: OperationError;
  /**
   * Unix time in which the data returned in the data property was obtained. If
   * OperationError has a value, this property will still have a valid value if valid
   * data was previously found.
   */
  momentOfLastCorrectUpdate: number;
}

/**
 * Response returned by specificNodeTrafficData.
 */
export class TrafficData {
  /**
   * Total amount of data sent by the node since it was started.
   */
  totalSent = 0;
  /**
   * Total amount of data received by the node since it was started.
   */
  totalReceived = 0;
  /**
   * Array with historic values of the totalSent property. Each value will be separeted by
   * the amount of time selected by the user for refreshing the data. It is not an exact history,
   * but the service will try it best to provided good data.
   */
  sentHistory: number[] = [];
  /**
   * Array with historic values of the totalReceived property. Each value will be separeted by
   * the amount of time selected by the user for refreshing the data. It is not an exact history,
   * but the service will try it best to provided good data.
   */
  receivedHistory: number[] = [];
}

/**
 * Keys for saving custom settings for the calls to the updater API endpoints.
 */
export enum UpdaterStorageKeys {
  /**
   * If has a value, at least one of the other keys have a value.
   */
  UseCustomSettings = 'updaterUseCustomSettings',
  Channel = 'updaterChannel',
  Version = 'updaterVersion',
  ArchiveURL = 'updaterArchiveURL',
  ChecksumsURL = 'updaterChecksumsURL',
}

/**
 * Allows to work with the nodes.
 */
@Injectable({
  providedIn: 'root'
})
export class NodeService {

  // How long the history arrays of the TrafficData instances will be.
  private readonly maxTrafficHistorySlots = 10;

  // Delay the service waits before requesting data.
  private dataRefreshDelay: number;

  constructor(
    private apiService: ApiService,
    private storageService: StorageService,
    private transportService: TransportService,
    private routeService: RouteService,
  ) {
    // Get the data refresing time set by the user.
    this.storageService.getRefreshTimeObservable().subscribe(val => {
      this.dataRefreshDelay = val * 1000;

      // If the service is currently automatically refreshing the data, restart the process.
      if (this.nodeListRefreshSubscription) {
        this.forceNodeListRefresh();
      }
      if (this.specificNodeRefreshSubscription) {
        this.forceSpecificNodeRefresh();
      }
    });
  }

  // Vars related to the node list.
  private nodeListSubject = new BehaviorSubject<BackendData>(null);
  private updatingNodeListSubject = new BehaviorSubject<boolean>(false);
  /**
   * Subscription for getting the node list. If it has a value, indicates that the
   * service is automatically refreshing the node list.
   */
  private nodeListRefreshSubscription: Subscription;
  /**
   * Subscription for the timer to stop refresing the node list. If it is valid, it means
   * a call to stop updating the data was made, but it is still pending.
   */
  private nodeListStopSubscription: Subscription;

  // Vars related to the specific node.
  private specificNodeSubject = new BehaviorSubject<BackendData>(null);
  private updatingSpecificNodeSubject = new BehaviorSubject<boolean>(false);
  private specificNodeTrafficDataSubject = new BehaviorSubject<TrafficData>(null);
  /**
   * Public key of the specific node this service must retrieve.
   */
  private specificNodeKey = '';
  /**
   * Last moment in which the specific node info was obtained following the specific intervals
   * defined by the user for updating the data. It allows to update the history data in a
   * consistent way.
   */
  private lastScheduledHistoryUpdateTime = 0;

  /**
   * Subscription for getting the specific node. If it has a value, indicates that the
   * service is automatically refreshing the node info.
   */
  private specificNodeRefreshSubscription: Subscription;
  /**
   * Subscription for the timer to stop refresing the specific node. If it is valid, it means
   * a call to stop updating the data was made, but it is still pending.
   */
  private specificNodeStopSubscription: Subscription;

  /**
   * Allows to get the node list. The list is periodically updated. It may emit null.
   */
  get nodeList(): Observable<BackendData> { return this.nodeListSubject.asObservable(); }
  /**
   * Allows to know if the service is currently updating the node list.
   */
  get updatingNodeList(): Observable<boolean> { return this.updatingNodeListSubject.asObservable(); }
  /**
   * Allows to get the specific node. The info is periodically updated. It may emit null.
   */
  get specificNode(): Observable<BackendData> { return this.specificNodeSubject.asObservable(); }
  /**
   * Allows to know if the service is currently updating the specific node.
   */
  get updatingSpecificNode(): Observable<boolean> { return this.updatingSpecificNodeSubject.asObservable(); }
  /**
   * Allows to get details about the data traffic of the specific node. The info is
   * periodically updated.
   */
  get specificNodeTrafficData(): Observable<TrafficData> { return this.specificNodeTrafficDataSubject.asObservable(); }

  /**
   * Makes the service start updating the node list. You must call this function before
   * using the nodeList observable.
   */
  startRequestingNodeList() {
    // If the previous procedure is still valid, continue it.
    if (this.nodeListStopSubscription && !this.nodeListStopSubscription.closed) {
      this.nodeListStopSubscription.unsubscribe();
      this.nodeListStopSubscription = null;

      return;
    }

    // Get for how many ms the saved data is still valid.
    const momentOfLastCorrectUpdate = this.nodeListSubject.value ? this.nodeListSubject.value.momentOfLastCorrectUpdate : 0;
    const remainingTime = this.calculateRemainingTime(momentOfLastCorrectUpdate);

    if (remainingTime === 0) {
      // Get the data from the backend.
      this.nodeListSubject.next(null);
      this.startDataSubscription(0, true);
    } else {
      // Use the data obtained the last time and schedule an update after the appropriate time.
      this.startDataSubscription(remainingTime, true);
    }
  }

  /**
   * Makes the service start updating a specific node. You must call this function before
   * using the specificNode observable.
   * @param publicKey Public key of the specific node to consult.
   */
  startRequestingSpecificNode(publicKey: string) {
    // If the previous procedure is still valid, continue it.
    if (this.specificNodeStopSubscription && !this.specificNodeStopSubscription.closed && this.specificNodeKey === publicKey) {
      this.specificNodeStopSubscription.unsubscribe();
      this.specificNodeStopSubscription = null;

      return;
    }

    // Get for how many ms the saved data is still valid.
    const momentOfLastCorrectUpdate = this.specificNodeSubject.value ? this.specificNodeSubject.value.momentOfLastCorrectUpdate : 0;
    const remainingTime = this.calculateRemainingTime(momentOfLastCorrectUpdate);

    // Reset the predefined data update intervals.
    this.lastScheduledHistoryUpdateTime = 0;

    if (this.specificNodeKey !== publicKey || remainingTime === 0) {
      // Get the data from the backend.
      this.specificNodeKey = publicKey;
      this.specificNodeTrafficDataSubject.next(new TrafficData());
      this.specificNodeSubject.next(null);
      this.startDataSubscription(0, false);
    } else {
      // Use the data obtained the last time and schedule an update after the appropriate time.
      this.startDataSubscription(remainingTime, false);
    }
  }

  /**
   * Calculates for how many ms the saved data is still valid before an update should be made.
   * @param momentOfLastCorrectUpdate Moment in which the data was saved.
   */
  private calculateRemainingTime(momentOfLastCorrectUpdate: number): number {
    if (momentOfLastCorrectUpdate < 1) {
      return 0;
    }

    let refreshDelay = this.dataRefreshDelay - (Date.now() - momentOfLastCorrectUpdate);
    if (refreshDelay < 0) {
      refreshDelay = 0;
    }

    return refreshDelay;
  }

  /**
   * Makes the service stop updating the node list.
   */
  stopRequestingNodeList() {
    if (this.nodeListRefreshSubscription) {
      this.nodeListStopSubscription = of(1).pipe(delay(4000)).subscribe(() => {
        this.nodeListRefreshSubscription.unsubscribe();
        this.nodeListRefreshSubscription = null;
      });
    }
  }

  /**
   * Makes the service stop updating the specific node.
   */
  stopRequestingSpecificNode() {
    if (this.specificNodeRefreshSubscription) {
      this.specificNodeStopSubscription = of(1).pipe(delay(4000)).subscribe(() => {
        this.specificNodeRefreshSubscription.unsubscribe();
        this.specificNodeRefreshSubscription = null;
      });
    }
  }

  /**
   * Starts periodically updating the node list or the specific node.
   * @param delayMs Delay before loading the data.
   * @param gettingNodeList True for getting the node list and false for getting the specific node.
   */
  private startDataSubscription(delayMs: number, gettingNodeList: boolean) {
    let updatingSubject: BehaviorSubject<boolean>;
    let dataSubject: BehaviorSubject<BackendData>;
    let operation: Observable<any>;

    if (gettingNodeList) {
      updatingSubject = this.updatingNodeListSubject;
      dataSubject = this.nodeListSubject;
      operation = this.getNodes();

      if (this.nodeListRefreshSubscription) {
        this.nodeListRefreshSubscription.unsubscribe();
      }
    } else {
      updatingSubject = this.updatingSpecificNodeSubject;
      dataSubject = this.specificNodeSubject;
      operation = this.getNode(this.specificNodeKey);

      if (this.specificNodeRefreshSubscription) {
        this.specificNodeRefreshSubscription.unsubscribe();
      }
    }

    const subscription = of(1).pipe(
      // Wait the requested delay.
      delay(delayMs),
      // Additional steps for making sure the UI shows the animation (important in case of quick errors).
      tap(() => updatingSubject.next(true)),
      delay(120),
      // Load the data.
      flatMap(() => operation))
    .subscribe(result => {
      updatingSubject.next(false);

      // Calculate the delay for the next update.
      let refrestTime: number;
      if (!gettingNodeList) {
        // Update the history values.
        this.updateTrafficData((result as Node).transports);

        // Wait for the amount of time pending for the next scheduled data update. This is needed
        // in case the data was updated before that by any reason.
        refrestTime = this.calculateRemainingTime(this.lastScheduledHistoryUpdateTime);
        if (refrestTime < 1000) {
          // Wait the normal time if there is just very lite or no time left for the next update.
          this.lastScheduledHistoryUpdateTime = Date.now();
          refrestTime = this.dataRefreshDelay;
        }
      } else {
        // Wait the normal time.
        refrestTime = this.dataRefreshDelay;
      }

      const newData: BackendData = {
        data: result,
        error: null,
        momentOfLastCorrectUpdate: Date.now(),
      };

      dataSubject.next(newData);

      // Schedule the next update.
      this.startDataSubscription(refrestTime, gettingNodeList);
    }, err => {
      updatingSubject.next(false);

      err = processServiceError(err);
      const newData: BackendData = {
        data: dataSubject.value && dataSubject.value.data ? dataSubject.value.data : null,
        error: err,
        momentOfLastCorrectUpdate: dataSubject.value ? dataSubject.value.momentOfLastCorrectUpdate : -1,
      };

      // If the specific node was not found, stop updating the data.
      const stopUpdating = !gettingNodeList && err.originalError && ((err.originalError as HttpErrorResponse).status === 400);

      // Schedule the next update.
      if (!stopUpdating) {
        this.startDataSubscription(AppConfig.connectionRetryDelay, gettingNodeList);
      }

      dataSubject.next(newData);
    });

    if (gettingNodeList) {
      this.nodeListRefreshSubscription = subscription;
    } else {
      this.specificNodeRefreshSubscription = subscription;
    }
  }

  /**
   * Updates the traffic data of the specific node.
   * @param transports Transports of the specific node.
   */
  private updateTrafficData(transports: Transport[]) {
    const currentData = this.specificNodeTrafficDataSubject.value;

    // Update the total data values.
    currentData.totalSent = 0;
    currentData.totalReceived = 0;
    if (transports && transports.length > 0) {
      currentData.totalSent = transports.reduce((total, transport) => total + transport.log.sent, 0);
      currentData.totalReceived = transports.reduce((total, transport) => total + transport.log.recv, 0);
    }

    // Update the history.
    if (currentData.sentHistory.length === 0) {
      // If the array is empty, just initialice the array with the only known value.
      for (let i = 0; i < this.maxTrafficHistorySlots; i++) {
        currentData.sentHistory[i] = currentData.totalSent;
        currentData.receivedHistory[i] = currentData.totalReceived;
      }
    } else {
      // Calculate how many slots should we move since the last time the history was updated.
      // This makes the intervals work well in case of normal updates, forced updates and
      // late updates due to errors.
      const TimeSinceLastHistoryUpdate = Date.now() - this.lastScheduledHistoryUpdateTime;
      let newSlotsNeeded =
        new BigNumber(TimeSinceLastHistoryUpdate).dividedBy(this.dataRefreshDelay).decimalPlaces(0, BigNumber.ROUND_FLOOR).toNumber();

      if (newSlotsNeeded > this.maxTrafficHistorySlots) {
        newSlotsNeeded = this.maxTrafficHistorySlots;
      }

      // Save the data in the correct slots.
      if (newSlotsNeeded === 0) {
        currentData.sentHistory[currentData.sentHistory.length - 1] = currentData.totalSent;
        currentData.receivedHistory[currentData.receivedHistory.length - 1] = currentData.totalReceived;
      } else {
        for (let i = 0; i < newSlotsNeeded; i++) {
          currentData.sentHistory.push(currentData.totalSent);
          currentData.receivedHistory.push(currentData.totalReceived);
        }
      }

      // Limit the history elements.
      if (currentData.sentHistory.length > this.maxTrafficHistorySlots) {
        currentData.sentHistory.splice(0, currentData.sentHistory.length - this.maxTrafficHistorySlots);
        currentData.receivedHistory.splice(0, currentData.receivedHistory.length - this.maxTrafficHistorySlots);
      }
    }

    this.specificNodeTrafficDataSubject.next(currentData);
  }

  /**
   * Makes the service immediately refresh the node list.
   */
  forceNodeListRefresh() {
    if (this.nodeListSubject.value) {
      // Make sure the current data is invalidated.
      this.nodeListSubject.value.momentOfLastCorrectUpdate = -1;
    }

    this.startDataSubscription(0, true);
  }

  /**
   * Makes the service immediately refresh the specific node.
   */
  forceSpecificNodeRefresh() {
    if (this.specificNodeSubject.value) {
      // Make sure the current data is invalidated.
      this.specificNodeSubject.value.momentOfLastCorrectUpdate = -1;
    }

    this.startDataSubscription(0, false);
  }

  /**
   * Gets the list of the nodes connected to the hypervisor.
   */
  private getNodes(): Observable<Node[]> {
    let nodes: Node[];

    return this.apiService.get('visors').pipe(mergeMap((result: Node[]) => {
      // Save the visor list.
      nodes = result || [];

      // Get the dmsg info.
      return this.apiService.get('dmsg');
    }), map((dmsgInfo: any[]) => {
      // Create a map to associate the dmsg info with the visors.
      const dmsgInfoMap = new Map<string, any>();
      dmsgInfo.forEach(info => dmsgInfoMap.set(info.public_key, info));

      // Process the node data and create a helper map.
      const obtainedNodes = new Map<string, Node>();
      const nodesToRegisterInLocalStorageAsOnline: string[] = [];
      nodes.forEach(node => {
        if (dmsgInfoMap.has(node.local_pk)) {
          node.dmsgServerPk = dmsgInfoMap.get(node.local_pk).server_public_key;
          node.roundTripPing = this.nsToMs(dmsgInfoMap.get(node.local_pk).round_trip);
        } else {
          node.dmsgServerPk = '-';
          node.roundTripPing = '-1';
        }

        node.ip = this.getAddressPart(node.tcp_addr, 0);
        node.port = this.getAddressPart(node.tcp_addr, 1);
        const labelInfo = this.storageService.getLabelInfo(node.local_pk);
        node.label =
          labelInfo && labelInfo.label ? labelInfo.label : this.storageService.getDefaultLabel(node.local_pk);

        obtainedNodes.set(node.local_pk, node);
        if (node.online) {
          nodesToRegisterInLocalStorageAsOnline.push(node.local_pk);
        }
      });

      this.storageService.includeVisibleLocalNodes(nodesToRegisterInLocalStorageAsOnline);

      const missingSavedNodes: Node[] = [];
      this.storageService.getSavedLocalNodes().forEach(node => {
        // If the backend did not return a saved node, add it to the response as an offline node.
        if (!obtainedNodes.has(node.publicKey) && !node.hidden) {
          const newNode: Node = new Node();
          newNode.local_pk = node.publicKey;
          const labelInfo = this.storageService.getLabelInfo(node.publicKey);
          newNode.label =
            labelInfo && labelInfo.label ? labelInfo.label : this.storageService.getDefaultLabel(node.publicKey);
          newNode.online = false;

          missingSavedNodes.push(newNode);
        }

        // If the backend returned a node, informed that it is offline and the saved data indicates
        // that the user deleted it from the node list in the past, remove it from the response.
        if (obtainedNodes.has(node.publicKey) && !obtainedNodes.get(node.publicKey).online && node.hidden) {
          obtainedNodes.delete(node.publicKey);
        }
      });

      nodes = [];
      obtainedNodes.forEach(value => nodes.push(value));
      nodes = nodes.concat(missingSavedNodes);

      return nodes;
    }));
  }

  /**
   * Converts a ns value to a ms string. It includes 2 decimals is the final value is less than 10.
   * @param time Value to convert.
   */
  private nsToMs(time: number) {
    let value = new BigNumber(time).dividedBy(1000000);

    if (value.isLessThan(10)) {
      value = value.decimalPlaces(2);
    } else {
      value = value.decimalPlaces(0);
    }

    return value.toString(10);
  }

  /**
   * Gets the details of a specific node.
   */
  private getNode(nodeKey: string): Observable<Node> {
    let currentNode: Node;

    // Get the basic node data.
    return this.apiService.get(`visors/${nodeKey}`).pipe(
      flatMap((node: Node) => {
        node.ip = this.getAddressPart(node.tcp_addr, 0);
        node.port = this.getAddressPart(node.tcp_addr, 1);
        const labelInfo = this.storageService.getLabelInfo(node.local_pk);
        node.label =
          labelInfo && labelInfo.label ? labelInfo.label : this.storageService.getDefaultLabel(node.local_pk);
        currentNode = node;

        // Needed for a change made to the names on the backend.
        if (node.apps) {
          node.apps.forEach(app => {
            app.name = (app as any).name ? (app as any).name : (app as any).app;
            app.autostart = (app as any).auto_start;
          });
        }

        // Get the dmsg info.
        return this.apiService.get('dmsg');
      }),
      flatMap((dmsgInfo: any[]) => {
        for (let i = 0; i < dmsgInfo.length; i++) {
          if (dmsgInfo[i].public_key === currentNode.local_pk) {
            currentNode.dmsgServerPk = dmsgInfo[i].server_public_key;
            currentNode.roundTripPing = this.nsToMs(dmsgInfo[i].round_trip);

            // Get the health info.
            return this.apiService.get(`visors/${nodeKey}/health`);
          }
        }

        currentNode.dmsgServerPk = '-';
        currentNode.roundTripPing = '-1';

        // Get the health info.
        return this.apiService.get(`visors/${nodeKey}/health`);
      }),
      flatMap((health: HealthInfo) => {
        currentNode.health = health;

        // Get the node uptime.
        return this.apiService.get(`visors/${nodeKey}/uptime`);
      }),
      flatMap((secondsOnline: string) => {
        currentNode.seconds_online = Math.floor(Number.parseFloat(secondsOnline));

        // Get the complete transports info.
        return this.transportService.getTransports(nodeKey);
      }),
      flatMap((transports: Transport[]) => {
        currentNode.transports = transports;

        // Get the complete routes info.
        return this.routeService.getRoutes(nodeKey);
      }),
      map((routes: Route[]) => {
        currentNode.routes = routes;

        return currentNode;
      })
    );
  }

  /**
   * Gets a part of the node address: the ip or the port.
   * @param tcpAddr Complete address.
   * @param part 0 for the ip or 1 for the port.
   */
  private getAddressPart(tcpAddr: string, part: number): string {
    const addressParts = tcpAddr.split(':');
    let port = tcpAddr;

    if (addressParts && addressParts.length === 2) {
      port = addressParts[part];
    }

    return port;
  }

  /**
   * Restarts a node.
   */
  reboot(nodeKey: string): Observable<any> {
    return this.apiService.post(`visors/${nodeKey}/restart`);
  }

  /**
   * Checks if a node is currently being updated. If no node key is provided, checks if the
   * hypervisor is currently being updated.
   */
  checkIfUpdating(nodeKey: string): Observable<any> {
    if (!nodeKey) {
      return this.apiService.get(`update/ws/running`);
    }

    return this.apiService.get(`visors/${nodeKey}/update/ws/running`);
  }

  /**
   * Checks if there are updates available for a node. If no node key is provided, checks if
   * there are updates available for the hypervisor.
   */
  checkUpdate(nodeKey: string): Observable<any> {
    if (!nodeKey) {
      return this.apiService.post(`update/available`);
    }

    return this.apiService.get(`visors/${nodeKey}/update/available`);
  }

  /**
   * Updates a node. If no node key is provided, updates the hypervisor.
   */
  update(nodeKey: string): Observable<any> {
    const body = {
      channel: 'stable'
      // channel: 'testing' // for debugging updater
    };

    // Use any custom settings saved by the user.
    const useCustomSettings = localStorage.getItem(UpdaterStorageKeys.UseCustomSettings);
    if (useCustomSettings) {
      const channel = localStorage.getItem(UpdaterStorageKeys.Channel);
      if (channel) { body['channel'] = channel; }
      const version = localStorage.getItem(UpdaterStorageKeys.Version);
      if (version) { body['version'] = version; }
      const archiveURL = localStorage.getItem(UpdaterStorageKeys.ArchiveURL);
      if (archiveURL) { body['archive_url'] = archiveURL; }
      const checksumsURL = localStorage.getItem(UpdaterStorageKeys.ChecksumsURL);
      if (checksumsURL) { body['checksums_url'] = checksumsURL; }
    }

    if (!nodeKey) {
      return this.apiService.ws(`update/ws`, body);
    }

    return this.apiService.ws(`visors/${nodeKey}/update/ws`, body);
  }
}
