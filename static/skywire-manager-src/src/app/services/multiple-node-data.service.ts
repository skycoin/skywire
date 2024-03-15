import { Injectable } from '@angular/core';
import { Observable, Subscription, BehaviorSubject, of } from 'rxjs';
import { delay, tap, mergeMap } from 'rxjs/operators';

import { StorageService } from './storage.service';
import { Node } from '../app.datatypes';
import { processServiceError } from '../utils/errors';
import { OperationError } from '../utils/operation-error';
import { AppConfig } from '../app.config';
import { NodeService } from './node.service';

/**
 * Data about the node list, returned by MultipleNodeDataService.
 */
export class MultipleNodesBackendData {
  /**
   * Node list. If the last operation for getting the data ended in an error, this property
   * may still have a previously obtained value. If no data has already been obtained, it
   * is null
   */
  data: Node[];
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
}

/**
 * Allows to get the list of nodes the current hypervisor is managing. It takes care of getting
 * the data and refreshing it in the intervals defined in the settings. After
 * startRequestingData() is called, it is constantly refreshing the data.
 */
@Injectable({
  providedIn: 'root'
})
export class MultipleNodeDataService {
  // Intervals (in ms) in which the service must refresh the data automatically.
  private dataRefreshDelay: number;
  // Subject for sending the data updates.
  dataSubject = new BehaviorSubject<MultipleNodesBackendData>(null);
  // Subscription for refreshing the data periodically.
  updateSubscription: Subscription;
  // Last data sent by dataSubject.
  lastEmitedData = new MultipleNodesBackendData();
  // If the getData function has already been called.
  firstCallToGetDataMade = false;

  constructor(
    private storageService: StorageService,
    private nodeService: NodeService,
  ) {
    // Get the data refresing time set by the user.
    this.storageService.getRefreshTimeObservable().subscribe(val => {
      this.dataRefreshDelay = val * 1000;

      // Refresh all data inmediatelly.
      this.forceRefresh();
    });
  }

  /**
   * Makes the service start returning the node list. Returns an observable for
   * getting the data.
   */
  startRequestingData(): Observable<MultipleNodesBackendData> {
    if (!this.firstCallToGetDataMade) {
      this.getData(0);
    }

    return this.dataSubject.asObservable();
  }

  /**
   * Makes the service stop returning the node list.
   */
  stopRequestingData() {
    if (this.updateSubscription) {
      this.updateSubscription.unsubscribe();
      this.firstCallToGetDataMade = false;
    }
  }

  /**
   * Starts periodically getting the node list.
   * @param delayMs Delay before loading the data.
   */
  private getData(delayMs: number) {
    this.firstCallToGetDataMade = true;

    if (this.updateSubscription) {
      this.updateSubscription.unsubscribe();
    }

    this.updateSubscription = of(1).pipe(
      // Wait the requested delay.
      delay(delayMs),
      // Additional steps for making sure the UI shows the animation (important in case of quick errors).
      tap(() => {
        this.lastEmitedData.updating = true;
        this.dataSubject.next(this.lastEmitedData);
      }),
      delay(120),
      // Load the data.
      mergeMap(() => this.nodeService.getNodes()))
    .subscribe(result => {
      // Send the event.
      this.lastEmitedData = {
        data: result,
        error: null,
        momentOfLastCorrectUpdate: Date.now(),
        updating: false
      };
      this.dataSubject.next(this.lastEmitedData);

      // Schedule the next update.
      this.getData(this.dataRefreshDelay);
    }, err => {
      err = processServiceError(err);

      // Send the event.
      this.lastEmitedData = {
        data: this.lastEmitedData.data,
        error: err,
        momentOfLastCorrectUpdate: this.lastEmitedData.momentOfLastCorrectUpdate,
        updating: false
      };
      this.dataSubject.next(this.lastEmitedData);

      // Schedule the next update.
      this.getData(AppConfig.connectionRetryDelay);
    });
  }

  /**
   * Makes the service immediately refresh the node list.
   */
  forceRefresh() {
    this.getData(0);
  }
}
