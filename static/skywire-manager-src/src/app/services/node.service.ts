import { Injectable } from '@angular/core';
import { HttpErrorResponse } from '@angular/common/http';
import { Observable, Subscription, BehaviorSubject, of } from 'rxjs';
import { flatMap, map, delay, tap } from 'rxjs/operators';
import BigNumber from 'bignumber.js';

import { StorageService } from './storage.service';
import { Node, Transport } from '../app.datatypes';
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
 * Data for knowing if the services of a node are working.
 */
export class HealthStatus {
  /**
   * If all services are working.
   */
  allServicesOk: boolean;
  /**
   * Details about the individual services.
   */
  services: HealthService[];
}

/**
 * Data for knowing if a service of a node is working.
 */
export class HealthService {
  /**
   * Name of the service, as a translatable var.
   */
  name: string;
  /**
   * If the service is working.
   */
  isOk: boolean;
  /**
   * Status text returned by the node.
   */
  originalValue: string;
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
    let remainingTime = this.calculateRemainingTime(momentOfLastCorrectUpdate);
    remainingTime = remainingTime > 0 ? remainingTime : 0;

    // Use the data obtained the last time and schedule an update after the appropriate time.
    this.startDataSubscription(remainingTime, true);
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
      // The delay allows to recover the connection if the user returns to the node page.
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

      // Cancel any pending stop operation.
      if (this.specificNodeStopSubscription) {
        this.specificNodeStopSubscription.unsubscribe();
        this.specificNodeStopSubscription = null;
      }

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
      currentData.totalSent = transports.reduce((total, transport) => total + transport.sent, 0);
      currentData.totalReceived = transports.reduce((total, transport) => total + transport.recv, 0);
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
    let nodes: Node[] = [];

    return this.apiService.get('visors-summary').pipe(map((result: any[]) => {
      // Save the visor list.
      if (result) {
        result.forEach(response => {
          const node = new Node();

          // Basic data.
          node.online = response.online;
          node.localPk = response.overview.local_pk;

          // Ip.
          if (response.overview && response.overview.local_ip && (response.overview.local_ip as string).trim()) {
            node.ip = response.overview.local_ip;
          } else {
            node.ip = null;
          }

          // Label.
          const labelInfo = this.storageService.getLabelInfo(node.localPk);
          node.label = labelInfo && labelInfo.label ? labelInfo.label : this.storageService.getDefaultLabel(node);

          // If the node is offline, there if no need for getting the rest of the data.
          if (!node.online) {
            node.dmsgServerPk = '';
            node.roundTripPing = '';
            nodes.push(node);

            return;
          }

          // Health data.
          node.health = {
            status: 200,
            addressResolver: response.health.address_resolver,
            routeFinder: response.health.route_finder,
            setupNode: response.health.setup_node,
            transportDiscovery: response.health.transport_discovery,
            uptimeTracker: response.health.uptime_tracker,
          };

          // DMSG info.
          node.dmsgServerPk = response.dmsg_stats.server_public_key;
          node.roundTripPing = this.nsToMs(response.dmsg_stats.round_trip);

          // Check if is hypervisor.
          node.isHypervisor = response.is_hypervisor;

          nodes.push(node);
        });
      }

      // Create lists with the nodes returned by the api.
      const obtainedNodes = new Map<string, Node>();
      const nodesToRegisterInLocalStorageAsOnline: string[] = [];
      const ipsToRegisterInLocalStorageAsOnline: string[] = [];
      nodes.forEach(node => {
        obtainedNodes.set(node.localPk, node);
        if (node.online) {
          nodesToRegisterInLocalStorageAsOnline.push(node.localPk);
          ipsToRegisterInLocalStorageAsOnline.push(node.ip);
        }
      });

      // Save all online nodes.
      this.storageService.includeVisibleLocalNodes(nodesToRegisterInLocalStorageAsOnline, ipsToRegisterInLocalStorageAsOnline);

      const missingSavedNodes: Node[] = [];
      this.storageService.getSavedLocalNodes().forEach(node => {
        // If the backend did not return a saved node, add it to the response as an offline node.
        if (!obtainedNodes.has(node.publicKey) && !node.hidden) {
          const newNode: Node = new Node();
          newNode.localPk = node.publicKey;
          const labelInfo = this.storageService.getLabelInfo(node.publicKey);
          newNode.label = labelInfo && labelInfo.label ? labelInfo.label : this.storageService.getDefaultLabel(newNode);
          newNode.online = false;
          newNode.dmsgServerPk = '';
          newNode.roundTripPing = '';

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
    // Get the node data.
    return this.apiService.get(`visors/${nodeKey}/summary`).pipe(
      map((response: any) => {
        const node = new Node();

        // Basic data.
        node.localPk = response.overview.local_pk;
        node.version = response.overview.build_info.version;
        node.secondsOnline = Math.floor(Number.parseFloat(response.uptime));
        node.minHops = response.min_hops;

        // Ip.
        if (response.overview.local_ip && (response.overview.local_ip as string).trim()) {
          node.ip = response.overview.local_ip;
        } else {
          node.ip = null;
        }

        // Label.
        const labelInfo = this.storageService.getLabelInfo(node.localPk);
        node.label = labelInfo && labelInfo.label ? labelInfo.label : this.storageService.getDefaultLabel(node);

        // Health info.
        node.health = {
          status: 200,
          addressResolver: response.health.address_resolver,
          routeFinder: response.health.route_finder,
          setupNode: response.health.setup_node,
          transportDiscovery: response.health.transport_discovery,
          uptimeTracker: response.health.uptime_tracker,
        };

        // Transports.
        node.transports = [];
        if (response.overview.transports) {
          (response.overview.transports as any[]).forEach(transport => {
            node.transports.push({
              isUp: transport.is_up,
              id: transport.id,
              localPk: transport.local_pk,
              remotePk: transport.remote_pk,
              type: transport.type,
              recv: transport.log.recv,
              sent: transport.log.sent,
            });
          });
        }

        // Routes.
        node.routes = [];
        if (response.routes) {
          (response.routes as any[]).forEach(route => {
            // Basic data.
            node.routes.push({
              key: route.key,
              rule: route.rule,
            });

            if (route.rule_summary) {
              // Rule summary.
              node.routes[node.routes.length - 1].ruleSummary = {
                keepAlive: route.rule_summary.keep_alive,
                ruleType: route.rule_summary.rule_type,
                keyRouteId: route.rule_summary.key_route_id,
              };

              // App fields, if any.
              if (route.rule_summary.app_fields && route.rule_summary.app_fields.route_descriptor) {
                node.routes[node.routes.length - 1].appFields = {
                  routeDescriptor: {
                    dstPk: route.rule_summary.app_fields.route_descriptor.dst_pk,
                    dstPort: route.rule_summary.app_fields.route_descriptor.dst_port,
                    srcPk: route.rule_summary.app_fields.route_descriptor.src_pk,
                    srcPort: route.rule_summary.app_fields.route_descriptor.src_port,
                  },
                };
              }

              // Forward fields, if any.
              if (route.rule_summary.forward_fields) {
                node.routes[node.routes.length - 1].forwardFields = {
                  nextRid: route.rule_summary.forward_fields.next_rid,
                  nextTid: route.rule_summary.forward_fields.next_tid,
                };

                if (route.rule_summary.forward_fields.route_descriptor) {
                  node.routes[node.routes.length - 1].forwardFields.routeDescriptor = {
                    dstPk: route.rule_summary.forward_fields.route_descriptor.dst_pk,
                    dstPort: route.rule_summary.forward_fields.route_descriptor.dst_port,
                    srcPk: route.rule_summary.forward_fields.route_descriptor.src_pk,
                    srcPort: route.rule_summary.forward_fields.route_descriptor.src_port,
                  };
                }
              }

              // Intermediary forward fields, if any.
              if (route.rule_summary.intermediary_forward_fields) {
                node.routes[node.routes.length - 1].intermediaryForwardFields = {
                  nextRid: route.rule_summary.intermediary_forward_fields.next_rid,
                  nextTid: route.rule_summary.intermediary_forward_fields.next_tid,
                };
              }
            }
          });
        }

        // Apps.
        node.apps = [];
        if (response.overview.apps) {
          (response.overview.apps as any[]).forEach(app => {
            node.apps.push({
              name: app.name,
              status: app.status,
              port: app.port,
              autostart: app.auto_start,
              args: app.args,
            });
          });
        }

        let dmsgServerFound = false;
        if (response.dmsg_stats) {
          node.dmsgServerPk = response.dmsg_stats.server_public_key;
          node.roundTripPing = this.nsToMs(response.dmsg_stats.round_trip);

          dmsgServerFound = true;
        }

        if (!dmsgServerFound) {
          node.dmsgServerPk = '-';
          node.roundTripPing = '-1';
        }

        return node;
      })
    );
  }

  /**
   * Restarts a node.
   */
  reboot(nodeKey: string): Observable<any> {
    return this.apiService.post(`visors/${nodeKey}/restart`);
  }

  /**
   * Checks if a node is currently being updated.
   */
  checkIfUpdating(nodeKey: string): Observable<any> {
    return this.apiService.get(`visors/${nodeKey}/update/ws/running`);
  }

  /**
   * Checks if there are updates available for a node.
   */
  checkUpdate(nodeKey: string): Observable<any> {
    let channel = 'stable';

    // Use the custom channel saved by the user, if any.
    const savedChannel = localStorage.getItem(UpdaterStorageKeys.Channel);
    channel = savedChannel ? savedChannel : channel;

    return this.apiService.get(`visors/${nodeKey}/update/available/${channel}`);
  }

  /**
   * Updates a node.
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

    return this.apiService.ws(`visors/${nodeKey}/update/ws`, body);
  }

  /**
   * Checks the data of a node and returns an object indicating the state of its services.
   */
  getHealthStatus(node: Node): HealthStatus {
    const response = new HealthStatus();
    response.allServicesOk = false;
    response.services = [];

    if (node.health) {
      // General status.
      let service: HealthService = {
        name: 'node.details.node-health.status',
        isOk: node.health.status && node.health.status === 200,
        originalValue: node.health.status + ''
      };
      response.services.push(service);

      // Transport discovery.
      service = {
        name: 'node.details.node-health.transport-discovery',
        isOk: node.health.transportDiscovery && node.health.transportDiscovery === 200,
        originalValue: node.health.transportDiscovery + ''
      };
      response.services.push(service);

      // Route finder.
      service = {
        name: 'node.details.node-health.route-finder',
        isOk: node.health.routeFinder && node.health.routeFinder === 200,
        originalValue: node.health.routeFinder + ''
      };
      response.services.push(service);

      // Setup node.
      service = {
        name: 'node.details.node-health.setup-node',
        isOk: node.health.setupNode && node.health.setupNode === 200,
        originalValue: node.health.setupNode + ''
      };
      response.services.push(service);

      // Uptime tracker.
      service = {
        name: 'node.details.node-health.uptime-tracker',
        isOk: node.health.uptimeTracker && node.health.uptimeTracker === 200,
        originalValue: node.health.uptimeTracker + ''
      };
      response.services.push(service);

      // Address resolver.
      service = {
        name: 'node.details.node-health.address-resolver',
        isOk: node.health.addressResolver && node.health.addressResolver === 200,
        originalValue: node.health.addressResolver + ''
      };
      response.services.push(service);

      // Check if any service is not working.
      response.allServicesOk = true;
      response.services.forEach(v => {
        if (!v.isOk) {
          response.allServicesOk = false;
        }
      });
    }

    return response;
  }
}
