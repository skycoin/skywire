import { Component, OnDestroy, OnInit, NgZone, Injector } from '@angular/core';
import { ActivatedRoute, Router } from '@angular/router';
import { Subscription } from 'rxjs/internal/Subscription';
import { Observable, ReplaySubject, timer } from 'rxjs';
import { HttpErrorResponse } from '@angular/common/http';

import { NodeService, BackendData } from '../../../services/node.service';
import { Node } from '../../../app.datatypes';
import { StorageService } from '../../../services/storage.service';
import { TabButtonData } from '../../layout/top-bar/top-bar.component';
import { SnackbarService } from '../../../services/snackbar.service';
import { NodeActionsHelper } from './actions/node-actions-helper';

/**
 * Main page used for showing the details of a node. It is in charge of loading
 * the node info. It does not show info directly, but acts as the container for
 * the subpages which shows the transport, apps and other details.
 */
@Component({
  selector: 'app-node',
  templateUrl: './node.component.html',
  styleUrls: ['./node.component.scss']
})
export class NodeComponent implements OnInit, OnDestroy {
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

  node: Node;
  notFound = false;

  // Values for the tab bar.
  titleParts = [];
  tabsData: TabButtonData[] = [];
  selectedTabIndex = -1;

  /**
   * Indicates if the subpage dedicated to show the node info (the same info shown in
   * right bar on large screens) is being shown.
   */
  showingInfo = false;
  /**
   * Indicates if the currently displayed subpage is one dedicated to show a full list
   * of elements (true) or if it is one dedicated only to show a sumary (false).
   */
  showingFullList = false;
  /**
   * Keeps track of the browser URL.
   */
  private lastUrl: string;

  private dataSubscription: Subscription;
  private updateTimeSubscription: Subscription;
  private navigationsSubscription: Subscription;

  // Vars for keeping track of the data updating.
  secondsSinceLastUpdate = 0;
  private lastUpdate = Date.now();
  updating = false;
  errorsUpdating = false;
  // True if the user manually requested the data to be updated and the update has still
  // not been made.
  lastUpdateRequestedManually = false;

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

  constructor(
    public storageService: StorageService,
    private nodeService: NodeService,
    private route: ActivatedRoute,
    private ngZone: NgZone,
    private snackbarService: SnackbarService,
    private injector: Injector,
    router: Router,
  ) {
    NodeComponent.nodeSubject = new ReplaySubject<Node>(1);
    NodeComponent.currentInstanceInternal = this;

    this.navigationsSubscription = router.events.subscribe(event => {
      if (event['urlAfterRedirects']) {
        NodeComponent.currentNodeKey = this.route.snapshot.params['key'];
        if (this.nodeActionsHelper) {
          this.nodeActionsHelper.setCurrentNodeKey(NodeComponent.currentNodeKey);
        }
        this.lastUrl = event['urlAfterRedirects'] as string;
        this.updateTabBar();
        this.navigationsSubscription.unsubscribe();

        // Load the data.
        this.nodeService.startRequestingSpecificNode(NodeComponent.currentNodeKey);
        this.startGettingData();
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
  }

  private updateTabBar() {

    // If showing one of the sumary pages (node info, transports or apps).
    if (
      this.lastUrl && (this.lastUrl.includes('/info') ||
      this.lastUrl.includes('/routing') ||
      (this.lastUrl.includes('/apps') && !this.lastUrl.includes('/apps-list')))) {

      this.titleParts = ['nodes.title', 'node.title'];

      this.tabsData = [
        {
          icon: 'info',
          label: 'node.tabs.info',
          // Hide the tab on large screens, as the info is shown on the right bar.
          onlyIfLessThanLg: true,
          linkParts: NodeComponent.currentNodeKey ? ['/nodes', NodeComponent.currentNodeKey, 'info'] : null,
        },
        {
          icon: 'shuffle',
          label: 'node.tabs.routing',
          linkParts: NodeComponent.currentNodeKey ? ['/nodes', NodeComponent.currentNodeKey, 'routing'] : null,
        },
        {
          icon: 'apps',
          label: 'node.tabs.apps',
          linkParts: NodeComponent.currentNodeKey ? ['/nodes', NodeComponent.currentNodeKey, 'apps'] : null,
        }
      ];

      // Check the URL to find out which tab should be shown as selected.
      this.selectedTabIndex = 1;
      this.showingInfo = false;
      if (this.lastUrl.includes('/info')) {
        this.selectedTabIndex = 0;
        this.showingInfo = true;
      }
      if (this.lastUrl.includes('/apps')) {
        this.selectedTabIndex = 2;
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
      this.lastUrl && (this.lastUrl.includes('/transports') ||
      this.lastUrl.includes('/routes') ||
      this.lastUrl.includes('/apps-list'))) {

      this.showingFullList = true;
      this.showingInfo = false;
      this.nodeActionsHelper = new NodeActionsHelper(this.injector, this.showingFullList);
      this.nodeActionsHelper.setCurrentNodeKey(NodeComponent.currentNodeKey);
      if (this.node) {
        this.nodeActionsHelper.setCurrentNode(this.node);
      }

      // Set the tabs bar header.
      let prefix = 'transports';
      if (this.lastUrl.includes('/routes')) {
        prefix = 'routes';
      } else if (this.lastUrl.includes('/apps-list')) {
        prefix = 'apps.apps-list';
      }
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

    this.nodeService.forceSpecificNodeRefresh();
  }

  /**
   * Starts getting the data from the backend.
   */
  private startGettingData() {
    // Detect when the service is updating the data.
    this.dataSubscription = this.nodeService.updatingSpecificNode.subscribe(val => this.updating = val);

    this.ngZone.runOutsideAngular(() => {
      // Get the node info.
      this.dataSubscription.add(this.nodeService.specificNode.subscribe((result: BackendData) => {
        this.ngZone.run(() => {
          if (result) {
            // If the data was obtained.
            if (result.data && !result.error) {
              this.node = result.data as Node;
              NodeComponent.nodeSubject.next(this.node);
              if (this.nodeActionsHelper) {
                this.nodeActionsHelper.setCurrentNode(this.node);
              }

              // Close any previous temporary loading error msg.
              this.snackbarService.closeCurrentIfTemporaryError();

              this.lastUpdate = result.momentOfLastCorrectUpdate;
              this.secondsSinceLastUpdate = Math.floor((Date.now() - result.momentOfLastCorrectUpdate) / 1000);
              this.errorsUpdating = false;

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
            }
          }
        });
      }));
    });
  }

  ngOnDestroy() {
    this.nodeService.stopRequestingSpecificNode();

    this.dataSubscription.unsubscribe();
    this.updateTimeSubscription.unsubscribe();
    this.navigationsSubscription.unsubscribe();

    NodeComponent.currentInstanceInternal = undefined;
    NodeComponent.currentNodeKey = undefined;

    NodeComponent.nodeSubject.complete();
    NodeComponent.nodeSubject = undefined;

    this.nodeActionsHelper.dispose();
  }
}
