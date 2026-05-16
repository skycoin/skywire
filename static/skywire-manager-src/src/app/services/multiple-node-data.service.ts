import { Injectable } from '@angular/core';
import { BehaviorSubject, Observable, Subscription, delay, mergeMap, of, tap } from 'rxjs';

import { StorageService } from './storage.service';
import { Node } from '../app.datatypes';
import { processServiceError } from '../utils/errors';
import { OperationError } from '../utils/operation-error';
import { AppConfig } from '../app.config';
import { NodeService, NodeSection } from './node.service';

/**
 * Data about the node list, returned by MultipleNodeDataService.
 */
export class MultipleNodesBackendData {
  /**
   * Flat node list, derived from sections by concatenating each
   * section's nodes (with cross-section visor de-duplication kept by
   * the consumer if needed — same visor PK can appear in multiple
   * sections by design). Existing components that consume the flat
   * list keep working unchanged.
   */
  data: Node[];
  /**
   * Per-hypervisor sections in tree form. First entry is the local
   * hypervisor; subsequent entries are sub-hypervisors. Consumed by
   * the main node list UI to render per-section tables with the
   * breadcrumb between them. See NodeService.getNodesTree() for the
   * source-of-truth contract.
   */
  sections: NodeSection[];
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
      // Load the data. Use the tree endpoint as the primary source —
      // it carries the per-sub-hypervisor structure the UI needs to
      // render multi-table layout. The flat `data` field is derived
      // from sections by concatenating each section's nodes; consumers
      // that don't care about sections keep working unchanged.
      mergeMap(() => this.nodeService.getNodesTree()))
    .subscribe((sections: NodeSection[]) => {
      const flat: Node[] = [];
      for (const s of sections) {
        for (const n of s.nodes) {
          flat.push(n);
        }
      }
      // Send the event.
      this.lastEmitedData = {
        data: flat,
        sections: sections,
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
        sections: this.lastEmitedData.sections,
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
