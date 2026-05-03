import { Component, OnDestroy, OnInit, NgZone, Injector, ChangeDetectorRef } from '@angular/core';
import { ActivatedRoute, Router } from '@angular/router';
import { Subscription } from 'rxjs/internal/Subscription';
import { Observable, ReplaySubject, delay, of, timer } from 'rxjs';
import { HttpErrorResponse } from '@angular/common/http';

import { Node } from '../../../app.datatypes';
import { StorageService } from '../../../services/storage.service';
import { TabButtonData } from '../../layout/top-bar/top-bar.component';
import { SnackbarService } from '../../../services/snackbar.service';
import { NodeActionsHelper } from './actions/node-actions-helper';
import { SingleNodeBackendData, SingleNodeDataService, TrafficData } from 'src/app/services/single-node-data.service';
import { PageBaseComponent } from 'src/app/utils/page-base';
import { AppComponent } from 'src/app/app.component';

/**
 * Main page used for showing the details of a node. It is in charge of loading
 * the node info. It does not show info directly, but acts as the container for
 * the subpages which shows the transport, apps and other details.
 */
@Component({
    selector: 'app-node',
    templateUrl: './node.component.html',
    styleUrls: ['./node.component.scss'],
    standalone: false
})
export class NodeComponent extends PageBaseComponent implements OnInit, OnDestroy {
  /**
   * Mantains a reference to the currently active instance of this page.
   */
  private static currentInstanceInternal: NodeComponent;
  /**
   * Public key of the node loaded in the currently active instance of this page.
   */
  private static currentNodeKey: string;
  /**
   * Lastest node data downloaded by the currently active instance of this page.
   */
  private static nodeSubject: ReplaySubject<Node>;
  /**
   * Lastest node traffic data downloaded by the currently active instance of this page.
   */
  private static trafficDataSubject: ReplaySubject<TrafficData>;


  // Keys for persisting the server data, to be able to restore the state after navigation.
  private readonly persistentDataResponseKey = 'serv-dat-response';

  node: Node;
  nodeLoaded = false;
  trafficData: TrafficData;
  notFound = false;

  // Values for the tab bar.
  titleParts = [];
  tabsData: TabButtonData[] = [];
  selectedTabIndex = -1;
  // Persistent visor identity rendered in the top bar — replaces
  // the translated "Visor details" title so the user can always see
  // which visor is loaded, on every tab.
  headerLabel = '';
  headerIdentifier = '';

  /**
   * Indicates if the currently displayed subpage is one dedicated to show a full list
   * of elements (true) or if it is one dedicated only to show a sumary (false).
   */
  showingFullList = false;
  /**
   * Keeps track of the browser URL.
   */
  private lastUrl: string;
  /**
   * Allows to know if a route event already fired.
   */
   private initialRouteEventFired = false;

  private dataSubscription: Subscription;
  private updateTimeSubscription: Subscription;
  private navigationsSubscription: Subscription;
  private initSubscription: Subscription;

  // Vars for keeping track of the data updating.
  secondsSinceLastUpdate = 0;
  private lastUpdate = Date.now();
  updating = false;
  errorsUpdating = false;
  // True if the user manually requested the data to be updated and the update has still
  // not been made.
  lastUpdateRequestedManually = false;
  // Set to true when we never successfully loaded data and errors persist.
  loadFailed = false;
  private consecutiveLoadErrors = 0;
  private instanceId = '';

  // Manages the options shown in the menu.
  nodeActionsHelper: NodeActionsHelper;

  /**
   * Ask the currently displayed instance of this page to reload the node data.
   */
  public static refreshCurrentDisplayedData() {
    if (NodeComponent.currentInstanceInternal) {
      NodeComponent.currentInstanceInternal.forceDataRefresh(false);
    }
  }

  /**
   * Gets the publick key of the node of the currently displayed instance of this page.
   */
  public static getCurrentNodeKey(): string {
    return NodeComponent.currentNodeKey;
  }

  /**
   * Gets the lastest node data downloaded by the currently active instance of this page.
   */
  public static get currentNode(): Observable<Node> {
    return NodeComponent.nodeSubject.asObservable();
  }

  /**
   * Gets the lastest node traffic data downloaded by the currently active instance of this page.
   */
  public static get currentTrafficData(): Observable<TrafficData> {
    return NodeComponent.trafficDataSubject.asObservable();
  }

  constructor(
    public storageService: StorageService,
    private singleNodeDataService: SingleNodeDataService,
    private route: ActivatedRoute,
    private ngZone: NgZone,
    private snackbarService: SnackbarService,
    private injector: Injector,
    private cdr: ChangeDetectorRef,
    router: Router,
  ) {
    super();

    NodeComponent.nodeSubject = new ReplaySubject<Node>(1);
    NodeComponent.trafficDataSubject = new ReplaySubject<TrafficData>(1);
    NodeComponent.currentInstanceInternal = this;
    this.instanceId = Math.random().toString(36).substring(7);
    console.log('[HV-DIAG] NodeComponent constructor, instance:', this.instanceId);

    this.navigationsSubscription = router.events.subscribe(event => {
      if (event['urlAfterRedirects']) {
        this.lastUrl = event['urlAfterRedirects'] as string;
        this.processRouteUpdate();
        this.initialRouteEventFired = true;
      }
    });
  }

  ngOnInit() {
    // Procedure to keep updated the variable that indicates how long ago the data was updated.
    this.ngZone.runOutsideAngular(() => {
      this.updateTimeSubscription =
        timer(5000, 5000).subscribe(() => this.ngZone.run(() => {
          this.secondsSinceLastUpdate = Math.floor((Date.now() - this.lastUpdate) / 1000);
        }));
    });

    this.initSubscription = of(0).pipe(delay(500)).subscribe(() => {
      if (!this.initialRouteEventFired) {
        this.lastUrl = window.location.href;
        this.processRouteUpdate();
      }
    });

    return super.ngOnInit();
  }

  private processRouteUpdate() {
    console.log('[HV-DIAG] processRouteUpdate called, instance id:', this.instanceId);
    NodeComponent.currentNodeKey = this.route.snapshot.params['key'];
    if (this.nodeActionsHelper) {
      this.nodeActionsHelper.setCurrentNodeKey(NodeComponent.currentNodeKey);
    }
    this.updateTabBar();
    this.navigationsSubscription.unsubscribe();

    // Load the data.
    this.startGettingData(true);
  }

  /**
   * Populate the header label + identifier from the loaded node so
   * the top bar always shows the visor's identity (replaces the
   * old translated "Visor details" string). Falls back to a short
   * PK if the user hasn't set a label.
   */
  private refreshHeader() {
    if (!this.node) {
      this.headerLabel = '';
      this.headerIdentifier = '';
      return;
    }
    const labelInfo = this.storageService.getLabelInfo(this.node.localPk);
    this.headerLabel = (labelInfo && labelInfo.label)
      ? labelInfo.label
      : (this.node.label || (this.node.localPk ? this.node.localPk.slice(0, 8) + '…' : ''));
    this.headerIdentifier = this.node.localPk || '';
  }

  private updateTabBar() {

    // If showing one of the sumary pages (node info, transports or apps).
    if (
      this.lastUrl && (this.lastUrl.includes('/info') ||
      this.lastUrl.includes('/routing') ||
      this.lastUrl.includes('/transports') ||
      this.lastUrl.includes('/rewards') ||
      this.lastUrl.includes('/skynet') ||
      this.lastUrl.includes('/resources') ||
      this.lastUrl.includes('/chat') ||
      this.lastUrl.includes('/dmsg') ||
      (this.lastUrl.includes('/apps') && !this.lastUrl.includes('/apps-list')))) {

      this.titleParts = ['nodes.title', 'node.title'];

      this.tabsData = [
        {
          icon: 'info',
          label: 'node.tabs.info',
          // Info is now a real tab on every screen size — the right
          // bar split-view that previously surfaced this content
          // has been removed.
          linkParts: NodeComponent.currentNodeKey ? ['/nodes', NodeComponent.currentNodeKey, 'info'] : null,
        },
        {
          icon: 'shuffle',
          label: 'node.tabs.routing',
          linkParts: NodeComponent.currentNodeKey ? ['/nodes', NodeComponent.currentNodeKey, 'routing'] : null,
        },
        {
          icon: 'swap_horiz',
          label: 'node.tabs.transports',
          linkParts: NodeComponent.currentNodeKey ? ['/nodes', NodeComponent.currentNodeKey, 'transports'] : null,
        },
        {
          icon: 'apps',
          label: 'node.tabs.apps',
          linkParts: NodeComponent.currentNodeKey ? ['/nodes', NodeComponent.currentNodeKey, 'apps'] : null,
        },
        {
          icon: 'monetization_on',
          label: 'node.tabs.rewards',
          linkParts: NodeComponent.currentNodeKey ? ['/nodes', NodeComponent.currentNodeKey, 'rewards'] : null,
        },
        {
          icon: 'public',
          label: 'node.tabs.skynet',
          linkParts: NodeComponent.currentNodeKey ? ['/nodes', NodeComponent.currentNodeKey, 'skynet'] : null,
        },
        {
          icon: 'memory',
          label: 'node.tabs.resources',
          linkParts: NodeComponent.currentNodeKey ? ['/nodes', NodeComponent.currentNodeKey, 'resources'] : null,
        },
        {
          icon: 'forum',
          label: 'node.tabs.chat',
          linkParts: NodeComponent.currentNodeKey ? ['/nodes', NodeComponent.currentNodeKey, 'chat'] : null,
        },
        {
          icon: 'device_hub',
          label: 'node.tabs.dmsg',
          linkParts: NodeComponent.currentNodeKey ? ['/nodes', NodeComponent.currentNodeKey, 'dmsg'] : null,
        }
      ];

      // Check the URL to find out which tab should be shown as selected.
      // Info is the default landing tab now (was Routing).
      this.selectedTabIndex = 0;
      if (this.lastUrl.includes('/routing')) {
        this.selectedTabIndex = 1;
      }
      if (this.lastUrl.includes('/transports')) {
        this.selectedTabIndex = 2;
      }
      if (this.lastUrl.includes('/apps') && !this.lastUrl.includes('/apps-list')) {
        this.selectedTabIndex = 3;
      }
      if (this.lastUrl.includes('/rewards')) {
        this.selectedTabIndex = 4;
      }
      if (this.lastUrl.includes('/skynet')) {
        this.selectedTabIndex = 5;
      }
      if (this.lastUrl.includes('/resources')) {
        this.selectedTabIndex = 6;
      }
      if (this.lastUrl.includes('/chat')) {
        this.selectedTabIndex = 7;
      }
      if (this.lastUrl.includes('/dmsg')) {
        this.selectedTabIndex = 8;
      }

      // Inform that the current subpage is not for showing a full list.
      this.showingFullList = false;
      this.nodeActionsHelper = new NodeActionsHelper(this.injector, this.showingFullList);
      this.nodeActionsHelper.setCurrentNodeKey(NodeComponent.currentNodeKey);
      if (this.node) {
        this.nodeActionsHelper.setCurrentNode(this.node);
      }

      // If showing a page dedicated to display a full list.
    } else if (
      this.lastUrl && (this.lastUrl.includes('/apps-list'))) {

      this.showingFullList = true;
      this.nodeActionsHelper = new NodeActionsHelper(this.injector, this.showingFullList);
      this.nodeActionsHelper.setCurrentNodeKey(NodeComponent.currentNodeKey);
      if (this.node) {
        this.nodeActionsHelper.setCurrentNode(this.node);
      }

      // /apps-list is the only remaining full-list page (transports
      // and routes are tabs now).
      const prefix = 'apps.apps-list';
      this.titleParts = ['nodes.title', 'node.title', prefix + '.title'];

      this.tabsData = [
        {
          icon: 'view_headline',
          label: prefix + '.list-title',
          linkParts: [],
        }
      ];

      this.selectedTabIndex = 0;
    } else {
      this.titleParts = [];
      this.tabsData = [];
    }
  }

  /**
   * Called when an option form the top bar is selected.
   * @param actionName Name of the selected option.
   */
  performAction(actionName: string) {
    // The helper object manages the event.
    this.nodeActionsHelper.performAction(actionName, NodeComponent.currentNodeKey);
  }

  /**
   * Makes the node info to be immediately refreshed.
   * @param requestedManually True if the data is going to be loaded because of a direct request
   * from the user.
   */
  forceDataRefresh(requestedManually = false) {
    if (requestedManually) {
      this.lastUpdateRequestedManually = true;
    }

    this.singleNodeDataService.forceSpecificNodeRefresh(NodeComponent.currentNodeKey);
  }

  /**
   * Retries loading after a load failure.
   */
  retryLoading() {
    this.loadFailed = false;
    this.consecutiveLoadErrors = 0;
    this.startGettingData(false);
  }

  /**
   * Starts getting the data from the backend.
   */
  private startGettingData(checkSavedData: boolean) {
    // Use saved data or get from the server. If there is no saved data, savedData is null.
    const savedData = checkSavedData ? this.getLocalValue(this.persistentDataResponseKey) : null;
    let nextOperation: Observable<any> = this.singleNodeDataService.startRequestingData(NodeComponent.currentNodeKey);
    if (savedData) {
      nextOperation = of(JSON.parse(savedData.value));
    }

    // Get the node info.
    this.dataSubscription = nextOperation.subscribe((result: SingleNodeBackendData) => {
      const t0 = performance.now();
      console.log('[HV-DIAG] data event received', {
        updating: result?.updating,
        hasData: !!result?.data,
        hasError: !!result?.error,
        transports: result?.data?.transports?.length ?? 'n/a',
        routes: result?.data?.routes?.length ?? 'n/a',
        apps: result?.data?.apps?.length ?? 'n/a',
      });

      if (!savedData) {
        const t1 = performance.now();
        this.saveLocalValue(this.persistentDataResponseKey, JSON.stringify(result));
        console.log('[HV-DIAG] saveLocalValue took', (performance.now() - t1).toFixed(1), 'ms');
      }

      this.updating = result ? result.updating : true;
      console.log('[HV-DIAG] updating:', this.updating, 'node set:', !!this.node, 'notFound:', this.notFound, 'loadFailed:', this.loadFailed);

      if (result && !result.updating) {
        // If the data was obtained.
        if (result.data && !result.error) {
          const tAssign = performance.now();
          this.ngZone.run(() => {
            this.node = result.data;
            this.trafficData = result.trafficData;
            this.nodeLoaded = true;
            this.refreshHeader();
          });
          console.log('[HV-DIAG] node assigned, instance:', this.instanceId, 'nodeLoaded:', this.nodeLoaded, 'node.localPk:', this.node?.localPk?.substring(0, 8), 'transports:', this.node?.transports?.length, 'routes:', this.node?.routes?.length);
          try {
            NodeComponent.nodeSubject.next(this.node);
          } catch(e) {
            console.error('[HV-DIAG] ERROR in nodeSubject.next:', e);
          }
          NodeComponent.trafficDataSubject.next(this.trafficData);
          if (this.nodeActionsHelper) {
            this.nodeActionsHelper.setCurrentNode(this.node);
          }

          // Close any previous temporary loading error msg.
          this.snackbarService.closeCurrentIfTemporaryError();

          this.lastUpdate = result.momentOfLastCorrectUpdate;
          this.secondsSinceLastUpdate = Math.floor((Date.now() - result.momentOfLastCorrectUpdate) / 1000);
          this.errorsUpdating = false;
          this.consecutiveLoadErrors = 0;
          this.loadFailed = false;
          AppComponent.currentInstance.hideDataProblemMsg();

          console.log('[HV-DIAG] data processing took', (performance.now() - tAssign).toFixed(1), 'ms');
          console.log('[HV-DIAG] AFTER ASSIGNMENT: this.node is', this.node ? 'SET' : 'NULL', 'this.notFound:', this.notFound, 'this.loadFailed:', this.loadFailed);
          this.cdr.detectChanges();

          if (this.lastUpdateRequestedManually) {
            // Show a confirmation msg.
            this.snackbarService.showDone('common.refreshed', null);
            this.lastUpdateRequestedManually = false;
          }

        // If there was an error while obtaining the data.
        } else if (result.error) {
          // If the node was not found, show a msg telling the user and stop the operation.
          if (result.error.originalError && ((result.error.originalError as HttpErrorResponse).status === 400)) {
            this.notFound = true;

            return;
          }

          this.consecutiveLoadErrors++;

          // If we never got data and errors persist, stop polling and show error state.
          if (!this.node && this.consecutiveLoadErrors >= 3) {
            this.loadFailed = true;
            this.singleNodeDataService.stopRequestingSpecificNode(NodeComponent.currentNodeKey);
            this.snackbarService.showError('common.loading-error', null, true, result.error);
            AppComponent.currentInstance.showDataProblemMsg();
            return;
          }

          // Show an error msg if it has not be done before during the current attempt to obtain the data.
          if (!this.errorsUpdating) {
            if (!this.node) {
              this.snackbarService.showError('common.loading-error', null, true, result.error);
            } else {
              this.snackbarService.showError('node.error-load', null, true, result.error);
            }
          }

          // Stop the loading indicator and show a warning icon.
          this.errorsUpdating = true;
          AppComponent.currentInstance.showDataProblemMsg();
        }
      }

      // If old saved data was used, repeat the operation, ignoring the saved data.
      if (savedData) {
        this.startGettingData(false);
      }
    });
  }

  ngOnDestroy() {
    console.log('[HV-DIAG] ngOnDestroy, instance:', this.instanceId);
    this.singleNodeDataService.stopRequestingSpecificNode(NodeComponent.currentNodeKey);

    this.dataSubscription.unsubscribe();
    this.updateTimeSubscription.unsubscribe();
    this.navigationsSubscription.unsubscribe();
    this.initSubscription.unsubscribe();

    NodeComponent.currentInstanceInternal = undefined;
    NodeComponent.currentNodeKey = undefined;

    NodeComponent.nodeSubject.complete();
    NodeComponent.nodeSubject = undefined;

    NodeComponent.trafficDataSubject.complete();
    NodeComponent.trafficDataSubject = undefined;

    this.nodeActionsHelper.dispose();
  }
}
