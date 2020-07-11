import { Injectable } from '@angular/core';
import { Observable, Subscription, BehaviorSubject, of } from 'rxjs';
import { flatMap, map, mergeMap, delay, tap } from 'rxjs/operators';
import BigNumber from 'bignumber.js';

import { StorageService } from './storage.service';
import { Node, Transport, Route, HealthInfo } from '../app.datatypes';
import { ApiService } from './api.service';
import { TransportService } from './transport.service';
import { RouteService } from './route.service';
import { processServiceError } from '../utils/errors';
import { OperationError } from '../utils/operation-error';

/**
 * Response returned by the node and node list observables.
 */
export interface BackendData {
  /**
   * Node or node list, if the last operation for getting the data did not end in an error.
   */
  data: Node[] | Node;
  /**
   * Error found while trying to get the data. If this property has a value, the data
   * property will be null.
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
 * Allows to work with the nodes.
 */
@Injectable({
  providedIn: 'root'
})
export class NodeService {

  // Delays the service waits before requesting data.
  private initialErrorRetryDelay = 3000;
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

  // Vars related to the specific node.
  private specificNodeSubject = new BehaviorSubject<BackendData>(null);
  private updatingSpecificNodeSubject = new BehaviorSubject<boolean>(false);
  /**
   * Public key of the specific node this service must retrieve.
   */
  private specificNodeKey = '';
  /**
   * Subscription for getting the specific node. If it has a value, indicates that the
   * service is automatically refreshing the node info.
   */
  private specificNodeRefreshSubscription: Subscription;

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
   * Makes the service start updating the node list. You must call this function before
   * using the nodeList observable.
   */
  startRequestingNodeList() {
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
    // Get for how many ms the saved data is still valid.
    const momentOfLastCorrectUpdate = this.specificNodeSubject.value ? this.specificNodeSubject.value.momentOfLastCorrectUpdate : 0;
    const remainingTime = this.calculateRemainingTime(momentOfLastCorrectUpdate);

    if (this.specificNodeKey !== publicKey || remainingTime === 0) {
      // Get the data from the backend.
      this.specificNodeKey = publicKey;
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
      this.nodeListRefreshSubscription.unsubscribe();
      this.nodeListRefreshSubscription = null;
    }
  }

  /**
   * Makes the service stop updating the specific node.
   */
  stopRequestingSpecificNode() {
    if (this.specificNodeRefreshSubscription) {
      this.specificNodeRefreshSubscription.unsubscribe();
      this.specificNodeRefreshSubscription = null;
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

      const newData: BackendData = {
        data: result,
        error: null,
        momentOfLastCorrectUpdate: Date.now(),
      };

      dataSubject.next(newData);

      // Schedule the next update.
      this.startDataSubscription(this.dataRefreshDelay, gettingNodeList);
    }, err => {
      updatingSubject.next(false);

      err = processServiceError(err);
      const newData: BackendData = {
        data: null,
        error: err,
        momentOfLastCorrectUpdate: dataSubject.value ? dataSubject.value.momentOfLastCorrectUpdate : -1,
      };

      // Schedule the next update.
      if (dataSubject.value && dataSubject.value.momentOfLastCorrectUpdate !== -1) {
        this.startDataSubscription(this.dataRefreshDelay, gettingNodeList);
      } else {
        this.startDataSubscription(this.initialErrorRetryDelay, gettingNodeList);
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
   * Makes the service immediately refresh the node list.
   */
  forceNodeListRefresh() {
    this.startDataSubscription(0, true);
  }

  /**
   * Makes the service immediately refresh the specific node.
   */
  forceSpecificNodeRefresh() {
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
        node.label = this.storageService.getNodeLabel(node.local_pk);

        obtainedNodes.set(node.local_pk, node);
      });

      const missingSavedNodes: Node[] = [];
      this.storageService.getNodes().forEach(node => {
        // If the backend did not return a saved node, add it to the response as an offline node.
        if (!obtainedNodes.has(node.publicKey) && !node.deleted) {
          const newNode: Node = new Node();
          newNode.local_pk = node.publicKey;
          newNode.label = node.label;
          newNode.online = false;

          missingSavedNodes.push(newNode);
        }

        // If the backend returned a node, informed that it is online and the saved data indicates that
        // the user deleted it from the node list in the past, remove it from the response.
        if (obtainedNodes.has(node.publicKey) && !obtainedNodes.get(node.publicKey).online && node.deleted) {
          obtainedNodes.delete(node.publicKey);
        }

        // If the user deleted an ofline node from the node list but now the backend says that it is online,
        // it will be shown in the node list again, so the "deleted" flag is removed in this code segment.
        if (obtainedNodes.has(node.publicKey) && obtainedNodes.get(node.publicKey).online && node.deleted) {
          this.storageService.changeNodeState(node.publicKey, false);
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
        node.label = this.storageService.getNodeLabel(node.local_pk);
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
   * Checks if there are updates available for a node.
   */
  checkUpdate(nodeKey: string): Observable<any> {
    return this.apiService.get(`visors/${nodeKey}/update/available`);
  }

  /**
   * Updates a node.
   */
  update(nodeKey: string): Observable<any> {
    return this.apiService.post(`visors/${nodeKey}/update`);
  }
}
