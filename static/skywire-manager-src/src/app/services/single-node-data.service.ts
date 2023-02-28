import { Injectable } from '@angular/core';
import { HttpErrorResponse } from '@angular/common/http';
import { Observable, Subscription, BehaviorSubject, of } from 'rxjs';
import { delay, tap, mergeMap } from 'rxjs/operators';
import BigNumber from 'bignumber.js';

import { StorageService } from './storage.service';
import { Node, Transport } from '../app.datatypes';
import { processServiceError } from '../utils/errors';
import { OperationError } from '../utils/operation-error';
import { AppConfig } from '../app.config';
import { NodeService } from './node.service';

/**
 * Data about a node, returned by SingleNodeDataService.
 */
export class SingleNodeBackendData {
  /**
   * Basic node data. If the last operation for getting the data ended in an
   * error, this property may still have a previously obtained value. If no data has already
   * been obtained, it is null
   */
  data: Node;
  /**
   * Error found while trying to get the data. It will only have a value if the last
   * try ended in an error.
   */
  error: OperationError;
  /**
   * Time (Date.now()) in which the data returned in the data property was obtained. If
   * the error proterty has a value, this property will still have a valid value if valid
   * data was previously found.
   */
  momentOfLastCorrectUpdate: number;
  /**
   * If the service is currently updating the data.
   */
  updating: boolean;
  /**
   * Stats about the data the node has sent and received.
   */
  trafficData = new TrafficData();
}

/**
 * Stats about the data a node has sent and received.
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
 * Data about a node for internal use inside SingleNodeDataService.
 */
export class NodeData {
  /**
   * Public key of the node.
   */
  pk: string;
  /**
   * Last SingleNodeBackendData instance that was emited with dataSubject.
   */
  lastEmitedData = new SingleNodeBackendData();
  /**
   * Subscription used for refreshing the node data periodically. Unsubscribe to
   * stop automatic refreshes.
   */
  updateSubscription: Subscription;
  /**
   * Subject used for sending events with data about this node.
   */
  dataSubject = new BehaviorSubject<SingleNodeBackendData>(null);
  /**
   * Moment in which the last automatic data refresh was scheduled. It is not the same as the
   * last moment in which the data was obtained, as the data could have not been obtained via
   * an automatic refresh (like when the user requests an update manually).
   */
  whenUpdateWasScheduled = 0;
  /**
   * Moment (Date.now()) in which a call to stop refreshing the node data and drop it from
   * the cache was made. If -1, no such call has been made.
   */
  stopRequestedDate = -1;
}

/**
 * Allows to get the data of a specific node. It takes care of getting the data and refreshing
 * it in the intervals defined in the settings. It also contains a cache that maintains node
 * data saved for some time, to avoid getting the data multiple times during navigation if not needed.
 */
@Injectable({
  providedIn: 'root'
})
export class SingleNodeDataService {
  // How long the history arrays of the TrafficData instances will be.
  private readonly maxTrafficHistorySlots = 10;
  // How much time entries should remain inside nodesMap after a stop request is made.
  private readonly expirationTime = 60000;

  // Intervals (in ms) in which the service must refresh the node data automatically.
  private dataRefreshDelay: number;

  // Map that works as cache, containing the data of the nodes this service has requested
  // data for. Entries here are updated automatically, even if a stop request was made, and
  // are purged after some time if a stop request was made.
  nodesMap = new Map<string, NodeData>();

  constructor(
    private storageService: StorageService,
    private nodeService: NodeService,
  ) {
    // Get the data refresing time set by the user.
    this.storageService.getRefreshTimeObservable().subscribe(val => {
      this.dataRefreshDelay = val * 1000;

      // Refresh all data inmediatelly.
      this.nodesMap.forEach(n => {
        this.forceSpecificNodeRefresh(n.pk);
      });
    });

    this.checkForExpired();
  }

  /**
   * Periodically checks all entries in nodesMap and removes all expired ones. Must be
   * called one tim e only, it calls itself automatically after that,
   */
  private checkForExpired() {
    of(1).pipe(delay(5000)).subscribe(() => {
      try {
        this.nodesMap.forEach(n => {
          this.finishIfExpired(n);
        });
      } catch (e) {}

      this.checkForExpired();
    });
  }

  /**
   * Makes the service start returning data for a specific node. Returns an observable for
   * getting the data.
   * @param publicKey Public key of the specific node to check.
   */
  startRequestingData(publicKey: string): Observable<SingleNodeBackendData> {
    // If the cache has info about the node, use the cached info. If not, create a
    // new entry.
    let nodeData = this.nodesMap.get(publicKey);
    if (!nodeData) {
      nodeData = new NodeData();
      nodeData.pk = publicKey;

      this.nodesMap.set(publicKey, nodeData);

      this.startDataSubscription(0, nodeData);
    } else {
      // Cancel any previous stop request.
      nodeData.stopRequestedDate = -1;
    }

    return nodeData.dataSubject.asObservable();
  }

  /**
   * Makes the service stop updating the data of a specific node. It is just a request, the
   * data will still be refreshed and maintained in cache for some time, to be able to access
   * it quickly if needed shortly after.
   */
  stopRequestingSpecificNode(publicKey: string) {
    const nodeData = this.nodesMap.get(publicKey);
    if (nodeData) {
      nodeData.stopRequestedDate = Date.now();
    }
  }

  /**
   * Starts periodically getting the data of a specific node.
   * @param delayMs Delay before loading the data.
   * @param nodeData Data about the node.
   */
  private startDataSubscription(delayMs: number, nodeData: NodeData) {
    if (nodeData.updateSubscription) {
      nodeData.updateSubscription.unsubscribe();
    }

    nodeData.updateSubscription = of(1).pipe(
      // Wait the requested delay.
      delay(delayMs),
      // Additional steps for making sure the UI shows the animation (important in case of quick errors).
      tap(() => {
        nodeData.lastEmitedData.updating = true;
        nodeData.dataSubject.next(nodeData.lastEmitedData);
      }),
      delay(120),
      // Load the data.
      mergeMap(() => this.nodeService.getNode(nodeData.pk)))
    .subscribe(result => {
      // Update the history values.
      this.updateTrafficData((result as Node).transports, nodeData.lastEmitedData.trafficData, nodeData.whenUpdateWasScheduled);

      // Wait for the amount of time pending for the next scheduled data update. This is needed
      // in case the data was updated before than expected for any reason.
      let refreshTime = this.calculateRemainingTime(nodeData.whenUpdateWasScheduled);
      if (refreshTime < 1000) {
        // Wait the normal time if there is just very lite or no time left for the next update.
        nodeData.whenUpdateWasScheduled = Date.now();
        refreshTime = this.dataRefreshDelay;
      }

      // Send the event.
      nodeData.lastEmitedData = {
        data: result,
        error: null,
        momentOfLastCorrectUpdate: Date.now(),
        updating: false,
        trafficData: nodeData.lastEmitedData.trafficData
      };
      nodeData.dataSubject.next(nodeData.lastEmitedData);

      // Schedule the next update.
      this.startDataSubscription(refreshTime, nodeData);
    }, err => {
      err = processServiceError(err);

      // Send the event.
      nodeData.lastEmitedData = {
        data: nodeData.lastEmitedData.data,
        error: err,
        momentOfLastCorrectUpdate: nodeData.lastEmitedData.momentOfLastCorrectUpdate,
        updating: false,
        trafficData: nodeData.lastEmitedData.trafficData
      };
      nodeData.dataSubject.next(nodeData.lastEmitedData);

      // If the specific node was not found, stop updating the data.
      const stopUpdating = err.originalError && ((err.originalError as HttpErrorResponse).status === 400);
      if (!stopUpdating) {
        // Schedule the next update.
        this.startDataSubscription(AppConfig.connectionRetryDelay, nodeData);
      } else {
        nodeData.dataSubject.complete();
        nodeData.updateSubscription.unsubscribe();
        this.nodesMap.delete(nodeData.pk);
      }
    });
  }

  /**
   * Calculates for how many ms a saved data is still valid before an update should be made.
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
   * Updates the traffic data.
   * @param transports Transports of the specific node.
   */
  private updateTrafficData(transports: Transport[], currentData: TrafficData, lastScheduledHistoryUpdateTime: number) {
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
      const TimeSinceLastHistoryUpdate = Date.now() - lastScheduledHistoryUpdateTime;
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
  }

  /**
   * Makes the service immediately refresh the specific node.
   */
  forceSpecificNodeRefresh(publicKey: string) {
    const nodeData = this.nodesMap.get(publicKey);
    if (nodeData) {
      this.startDataSubscription(0, nodeData);
    }
  }

  /**
   * Checks a node to see if a call for stopping updates was made and if it is already expired.
   * If so, the updates are stopped and the node is removed form nodesMap.
   */
  private finishIfExpired(nodeData: NodeData) {
    if (nodeData.stopRequestedDate > 0) {
      if (Date.now() - nodeData.stopRequestedDate > this.expirationTime) {
        nodeData.dataSubject.complete();
        nodeData.updateSubscription.unsubscribe();

        this.nodesMap.delete(nodeData.pk);
      }
    }
  }
}
