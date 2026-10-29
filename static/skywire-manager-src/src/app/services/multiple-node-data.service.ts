import { Injectable } from '@angular/core';
import { BehaviorSubject, EMPTY, Observable, Subscription, catchError, delay, mergeMap, of, tap } from 'rxjs';

import { StorageService } from './storage.service';
import { Node } from '../app.datatypes';
import { processServiceError } from '../utils/errors';
import { OperationError } from '../utils/operation-error';
import { AppConfig } from '../app.config';
import { NodeService, NodeSection } from './node.service';
import { LiveService, LiveUnavailableError } from './live.service';

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
  data!: Node[];
  /**
   * Per-hypervisor sections in tree form. First entry is the local
   * hypervisor; subsequent entries are sub-hypervisors. Consumed by
   * the main node list UI to render per-section tables with the
   * breadcrumb between them. See NodeService.getNodesTree() for the
   * source-of-truth contract.
   */
  sections!: NodeSection[];
  /**
   * Error found while trying to get the data. It will only have a value if the last
   * try ended in an error.
   */
  error!: OperationError | null;
  /**
   * Time (Date.now()) in which the data returned in the data property was obtained. If
   * the error proterty has a value, this property will still have a valid value if valid
   * data was previously found.
   */
  momentOfLastCorrectUpdate!: number;
  /**
   * If the service is currently updating the data.
   */
  updating!: boolean;
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
  private dataRefreshDelay!: number;
  // Subject for sending the data updates.
  dataSubject = new BehaviorSubject<MultipleNodesBackendData | null>(null);
  // Subscription for refreshing the data periodically.
  updateSubscription!: Subscription;
  // Last data sent by dataSubject.
  lastEmitedData = new MultipleNodesBackendData();
  // If the getData function has already been called.
  firstCallToGetDataMade = false;

  constructor(
    private storageService: StorageService,
    private nodeService: NodeService,
    private live: LiveService,
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
  startRequestingData(): Observable<MultipleNodesBackendData | null> {
    if (!this.firstCallToGetDataMade) {
      this.startUpdates();
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

  /** Follows the list over the live socket when possible, else polls. */
  private startUpdates() {
    if (this.live.usable) {
      this.startLiveSubscription();
    } else {
      this.getData(0);
    }
  }

  /**
   * Gets the list pushed over the live socket, which sends it at once and then
   * whenever it changes. A new subscription also serves as a forced refresh.
   * Falls back to polling if the socket cannot be used.
   */
  private startLiveSubscription() {
    this.firstCallToGetDataMade = true;

    if (this.updateSubscription) {
      this.updateSubscription.unsubscribe();
    }

    this.updateSubscription = this.live.subscribe('/api/visors-tree-summary', this.dataRefreshDelay).pipe(
      mergeMap(ev => {
        if (ev.status === 429) {
          throw new LiveUnavailableError();
        }
        if (ev.status >= 200 && ev.status < 300) {
          return of(this.nodeService.nodesTreeFromResponse(JSON.parse(ev.body)));
        }

        // Ask over HTTP once, so the error is processed (and a lost session
        // redirects to the login page) exactly as when polling.
        return this.nodeService.getNodesTree().pipe(catchError(err => {
          this.emitError(err);

          return EMPTY;
        }));
      }),
    ).subscribe({
      next: sections => this.emitSections(sections),
      error: () => this.getData(0),
    });
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
      this.emitSections(sections);

      // Schedule the next update.
      this.getData(this.dataRefreshDelay);
    }, err => {
      this.emitError(err);

      // Schedule the next update.
      this.getData(AppConfig.connectionRetryDelay);
    });
  }

  private emitSections(sections: NodeSection[]) {
    // Flat list = section 0 (LOCAL hypervisor's directly-connected
    // visors) only. Visors connected only to a sub-hypervisor live
    // in their own section's table, never in the main flat list —
    // a visor should appear in the list of the hypervisor it's
    // actually connected to and nowhere else. Concatenating
    // sub-sections into the flat list caused visors connected to
    // both local and a sub-hyp to render twice in the main table,
    // and dragged the section's own ★ flag into the main table
    // where it was visually wrong.
    const flat: Node[] = sections.length > 0 ? sections[0].nodes.slice() : [];
    // Send the event.
    this.lastEmitedData = {
      data: flat,
      sections: sections,
      error: null,
      momentOfLastCorrectUpdate: Date.now(),
      updating: false
    };
    this.dataSubject.next(this.lastEmitedData);
  }

  private emitError(err: any) {
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
  }

  /**
   * Makes the service immediately refresh the node list.
   */
  forceRefresh() {
    this.startUpdates();
  }
}
