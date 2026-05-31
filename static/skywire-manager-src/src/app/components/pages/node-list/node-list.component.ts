import { Component, OnDestroy, OnInit, NgZone } from '@angular/core';
import { CdkDragDrop, moveItemInArray } from '@angular/cdk/drag-drop';
import { Observable, Subscription, catchError, mergeMap, of, timer } from 'rxjs';
import { MatDialog } from '@angular/material/dialog';
import { Router, ActivatedRoute } from '@angular/router';
import { TranslateService } from '@ngx-translate/core';

import { NodeService, NodeSection, KnownHealthStatuses } from '../../../services/node.service';
import { Node } from '../../../app.datatypes';
import { AuthService, AuthStates } from '../../../services/auth.service';
import { EditLabelComponent } from '../../layout/edit-label/edit-label.component';
import { StorageService, LabeledElementTypes } from '../../../services/storage.service';
import { TabButtonData, MenuOptionData } from '../../layout/top-bar/top-bar.component';
import { homeTabsData } from 'src/app/utils/home-tabs';
import { SnackbarService } from '../../../services/snackbar.service';
import GeneralUtils from 'src/app/utils/generalUtils';
import { SelectOptionComponent, SelectableOption } from '../../layout/select-option/select-option.component';
import { ClipboardService } from 'src/app/services/clipboard.service';
import { AppConfig } from 'src/app/app.config';
import { FilterProperties, FilterFieldTypes } from 'src/app/utils/filters';
import { SortingModes, SortingColumn, DataSorter } from 'src/app/utils/lists/data-sorter';
import { DataFilterer } from 'src/app/utils/lists/data-filterer';
import { NodeData, UpdateAllComponent } from '../../layout/update-all/update-all.component';
import { BulkRewardAddressChangerComponent, BulkRewardAddressParams, NodeToEditData } from '../../layout/bulk-reward-address-changer/bulk-reward-address-changer.component';
import { MultipleNodeDataService, MultipleNodesBackendData } from 'src/app/services/multiple-node-data.service';
import { PageBaseComponent } from 'src/app/utils/page-base';
import { AppComponent } from 'src/app/app.component';
import { RewardService, VisorRewardData } from 'src/app/services/reward.service';

/**
 * Page for showing the node list.
 */
@Component({
    selector: 'app-node-list',
    templateUrl: './node-list.component.html',
    styleUrls: ['./node-list.component.scss'],
    standalone: false
})
export class NodeListComponent extends PageBaseComponent implements OnInit, OnDestroy {
  // Keys for persisting the server data, to be able to restore the state after navigation.
  private readonly persistentServerDataResponseKey = 'serv-dat-response';

  // Small texts for identifying the list, needed for the helper objects.
  private readonly nodesListId = 'nl';
  private readonly dmsgListId = 'dl';

  // Vars with the data of the columns used for sorting the data.
  hypervisorSortData = new SortingColumn(['isHypervisor'], 'nodes.hypervisor', SortingModes.Boolean);
  stateSortData = new SortingColumn(['online'], 'nodes.state', SortingModes.Boolean);
  labelSortData = new SortingColumn(['label'], 'nodes.label', SortingModes.Text);
  keySortData = new SortingColumn(['localPk'], 'nodes.key', SortingModes.Text);
  versionSortData = new SortingColumn(['version'], 'nodes.version', SortingModes.Text);
  configVersionSortData = new SortingColumn(['configVersion'], 'nodes.config-version', SortingModes.Text);
  dmsgServerSortData = new SortingColumn(['dmsgServerPk'], 'nodes.dmsg-server', SortingModes.Text, ['dmsgServerPk_label']);
  pingSortData = new SortingColumn(['roundTripPing'], 'nodes.ping', SortingModes.Number);
  // New sort columns — text sorts use country code / reward address
  // (no additional data fetch); count sorts use pre-computed counts
  // populated by annotateForSort() below before handing off to the
  // filterer/sorter pipeline.
  ipLocationSortData = new SortingColumn(['countryCode'], 'nodes.ip-location', SortingModes.Text);
  transportsCountSortData = new SortingColumn(['transportsCount'], 'nodes.transports', SortingModes.Number);
  servicesCountSortData = new SortingColumn(['servicesCount'], 'nodes.services', SortingModes.Number);
  rewardAddressSortData = new SortingColumn(['rewardsAddress'], 'nodes.reward', SortingModes.Text);

  /**
   * Persistent user-defined ordering. When non-empty, applied after
   * the sorter+filterer so user drag-drop wins over the column sort
   * for the rows the user explicitly arranged. Rows not in the saved
   * order keep the sorter's relative order, appended after the
   * explicit rows. Cleared per-column when the user clicks a column
   * header to sort. Persisted in localStorage under
   * customNodeOrderKey so the order survives reloads.
   */
  customNodeOrder: string[] = [];
  private readonly customNodeOrderKey = 'nl-custom-order';

  private dataSortedSubscription: Subscription;
  private dataFiltererSubscription: Subscription;
  // Objects in charge of sorting and filtering the data.
  dataSorter: DataSorter;
  dataFilterer: DataFilterer;

  loading = true;
  dataSource: Node[] = [];
  tabsData: TabButtonData[] = [];
  options: MenuOptionData[] = [];
  showDmsgInfo = false;
  showRewardsInfo = false;
  canLogOut = true;

  // Reward data
  rewardDataMap: Map<string, VisorRewardData> = new Map();
  rewardDates: string[] = [];
  rewardDateHeaders: string[] = [];
  rewardDataLoading = false;
  rewardDataLoaded = false;

  // Vars for the pagination functionality.
  allNodes: Node[];
  filteredNodes: Node[];
  nodesToShow: Node[];
  hasOfflineNodes = false;
  numberOfPages = 1;
  currentPage = 1;
  // Used as a helper var, as the URL is read asynchronously.
  currentPageInUrl = 1;

  // Per-hypervisor sections from the tree endpoint (#2633). The
  // flat main table renders sections[0] (local) + every remote
  // visor; sub-sections (sections[1..]) get their own header +
  // compact node list below the main table so operators see each
  // remote hypervisor's directly-connected visors at a glance.
  sections: NodeSection[] = [];
  // Local hypervisor PK — convenience accessor for the title bar
  // (derived from sections[0]). Empty until first data arrives.
  localHypervisorPk = '';

  /**
   * Sub-hypervisor sections (everything beyond the local one).
   * Returns sections[1..] so the template can iterate without
   * needing per-row $index checks. Empty until the tree response
   * lands or when only the local section exists (no remote
   * hypervisors connected). Filtering out `subError` sections is
   * intentionally skipped here — even an unreachable sub-hypervisor
   * is informative to display, with its error rendered inline.
   */
  get subSections(): NodeSection[] {
    return this.sections.length > 1 ? this.sections.slice(1) : [];
  }

  // Array with the properties of the columns that can be used for filtering the data.
  filterProperties: FilterProperties[] = [
    {
      filterName: 'nodes.filter-dialog.online',
      keyNameInElementsArray: 'online',
      type: FilterFieldTypes.Select,
      printableLabelsForValues: [
        {
          value: '',
          label: 'nodes.filter-dialog.online-options.any',
        },
        {
          value: 'true',
          label: 'nodes.filter-dialog.online-options.online',
        },
        {
          value: 'false',
          label: 'nodes.filter-dialog.online-options.offline',
        }
      ],
    },
    {
      filterName: 'nodes.filter-dialog.label',
      keyNameInElementsArray: 'label',
      type: FilterFieldTypes.TextInput,
      maxlength: 100,
    },
    {
      filterName: 'nodes.filter-dialog.key',
      keyNameInElementsArray: 'localPk',
      type: FilterFieldTypes.TextInput,
      maxlength: 66,
    },
    {
      filterName: 'nodes.filter-dialog.dmsg',
      keyNameInElementsArray: 'dmsgServerPk',
      secondaryKeyNameInElementsArray: 'dmsgServerPk_label',
      type: FilterFieldTypes.TextInput,
      maxlength: 66,
    }
  ];

  private authVerificationSubscription: Subscription;
  private dataSubscription: Subscription;
  private updateTimeSubscription: Subscription;
  private updateSubscription: Subscription;
  private navigationsSubscription: Subscription;
  private languageSubscription: Subscription;

  // Vars for keeping track of the data updating.
  secondsSinceLastUpdate = 0;
  private lastUpdate = Date.now();
  updating = false;
  errorsUpdating = false;
  // True if the user manually requested the data to be updated and the update has still
  // not been made.
  lastUpdateRequestedManually = false;

  labeledElementTypes = LabeledElementTypes;

  constructor(
    private nodeService: NodeService,
    private multipleNodeDataService: MultipleNodeDataService,
    private router: Router,
    private dialog: MatDialog,
    private authService: AuthService,
    public storageService: StorageService,
    private ngZone: NgZone,
    private snackbarService: SnackbarService,
    private clipboardService: ClipboardService,
    private translateService: TranslateService,
    private rewardService: RewardService,
    route: ActivatedRoute,
  ) {
    super();

    // Configure the options menu shown in the top bar.
    this.updateOptionsMenu();

    // Check if logout button must be removed.
    this.authVerificationSubscription = this.authService.checkLogin().subscribe(response => {
      this.canLogOut = response !== AuthStates.AuthDisabled;
      this.updateOptionsMenu();
    });

    // Show the rewards info if the rewards url was used (also catch old dmsg urls via redirect).
    this.showRewardsInfo = this.router.url.indexOf('rewards') !== -1;
    // Keep showDmsgInfo false — DMSG tab is replaced by Rewards
    this.showDmsgInfo = false;

    // Remove the DMSG filtering options (last entry) since DMSG tab is gone.
    this.filterProperties.splice(this.filterProperties.length - 1);

    // Initialize the data sorter.
    const sortableColumns: SortingColumn[] = [
      this.hypervisorSortData,
      this.stateSortData,
      this.labelSortData,
      this.ipLocationSortData,
      this.transportsCountSortData,
      this.keySortData,
      this.versionSortData,
      this.configVersionSortData,
      this.servicesCountSortData,
      this.rewardAddressSortData,
    ];
    const listId = this.showRewardsInfo ? 'rl' : this.nodesListId;
    this.dataSorter = new DataSorter(
      this.dialog, this.translateService, this.storageService, sortableColumns, 3, listId
    );
    this.dataSortedSubscription = this.dataSorter.dataSorted.subscribe(() => {
      // When this happens, the data in allNodes has already been sorted.
      this.recalculateElementsToShow();
    });

    this.dataFilterer = new DataFilterer(
      this.dialog, route, this.router, this.filterProperties, listId
    );
    this.dataFiltererSubscription = this.dataFilterer.dataFiltered.subscribe(data => {
      this.filteredNodes = data;

      // Check if there are offline nodes.
      this.hasOfflineNodes = false;
      this.filteredNodes.forEach(node => {
        if (!node.online) {
          this.hasOfflineNodes = true;
        }
      });

      this.dataSorter.setData(this.filteredNodes);
    });

    // Get the page requested in the URL.
    this.navigationsSubscription = route.paramMap.subscribe(params => {
      if (params.has('page')) {
        let selectedPage = Number.parseInt(params.get('page'), 10);
        if (isNaN(selectedPage) || selectedPage < 1) {
          selectedPage = 1;
        }

        this.currentPageInUrl = selectedPage;

        this.recalculateElementsToShow();
      }
    });

    this.tabsData = homeTabsData();

    // Refresh the data after languaje changes, to ensure the labels used for filtering
    // are updated.
    this.languageSubscription = this.translateService.onLangChange.subscribe(() => {
      this.multipleNodeDataService.forceRefresh();
    });
  }

  /**
   * Configures the options menu shown in the top bar.
   */
  private updateOptionsMenu() {
    this.options = [];

    this.options.push({
      name: 'nodes.modify-rewards-all',
      actionName: 'modifyRewardsAll',
      icon: 'edit'
    });

    // TODO: remove if the option will not be added again. Delete the translatable strings too.
    /*
    this.options.push({
      name: 'nodes.update-all',
      actionName: 'updateAll',
      icon: 'get_app'
    });
    */
    if (this.canLogOut) {
      this.options.push({
        name: 'common.logout',
        actionName: 'logout',
        icon: 'power_settings_new'
      });
    }
  }

  ngOnInit() {
    // Restore the user's drag-drop custom order from localStorage so
    // a reload preserves what they explicitly arranged. Empty / parse
    // failure → keep customNodeOrder as the empty default (pure
    // column-sort behavior).
    const savedOrder = this.getLocalValue(this.customNodeOrderKey);
    if (savedOrder && savedOrder.value) {
      try {
        const parsed = JSON.parse(savedOrder.value);
        if (Array.isArray(parsed) && parsed.every(p => typeof p === 'string')) {
          this.customNodeOrder = parsed;
        }
      } catch {
        // Ignore parse failures — leaves customNodeOrder empty.
      }
    }

    // Load the data.
    this.startGettingData(true);

    // Procedure to keep updated the variable that indicates how long ago the data was updated.
    this.ngZone.runOutsideAngular(() => {
      this.updateTimeSubscription =
        timer(5000, 5000).subscribe(() => this.ngZone.run(() => {
          this.secondsSinceLastUpdate = Math.floor((Date.now() - this.lastUpdate) / 1000);
        }));
    });

    return super.ngOnInit();
  }

  ngOnDestroy() {
    this.authVerificationSubscription.unsubscribe();
    this.dataSubscription.unsubscribe();
    this.updateTimeSubscription.unsubscribe();
    this.navigationsSubscription.unsubscribe();
    this.languageSubscription.unsubscribe();

    if (this.updateSubscription) {
      this.updateSubscription.unsubscribe();
    }

    this.dataSortedSubscription.unsubscribe();
    this.dataSorter.dispose();

    this.dataFiltererSubscription.unsubscribe();
    this.dataFilterer.dispose();
  }

  /**
   * Called when an option form the top bar is selected.
   * @param actionName Name of the selected option, as defined in the this.options array.
   */
  performAction(actionName: string) {
    if (actionName === 'logout') {
      this.logout();
    } else if (actionName === 'updateAll') {
      this.updateAll();
    } else if (actionName === 'modifyRewardsAll') {
      this.changeRewardsToAll();
    }
  }

  /**
   * Returns the scss class to be used to show the current status of the node.
   * @param forDot If true, returns a class for creating a colored dot. If false,
   * returns a class for a colored text.
   */
  nodeStatusClass(node: Node, forDot: boolean): string {
    if (node.online) {
      if (node.health && node.health.servicesHealth === KnownHealthStatuses.Unhealthy) {
        return forDot ? 'dot-yellow blinking' : 'yellow-text';
      } else if (node.health && node.health.servicesHealth === KnownHealthStatuses.Healthy) {
        return forDot ? 'dot-green' : 'green-text';
      } else {
        return forDot ? 'dot-outline-gray' : '';
      }
    } else if (node.isStale) {
      // Offline but the row carries last-known data from the
      // hypervisor's summary cache. Distinguish from plain offline.
      return forDot ? 'dot-red dimmed' : 'red-text dimmed';
    } else {
      return forDot ? 'dot-red' : 'red-text';
    }
  }

  /**
   * Returns the text to be used to indicate the current status of the node.
   * @param forTooltip If true, returns a text for a tooltip. If false, returns a
   * text for the node list shown on small screens.
   */
  nodeStatusText(node: Node, forTooltip: boolean): string {
    if (node.online) {
      if (node.health && node.health.servicesHealth === KnownHealthStatuses.Healthy) {
        return 'node.statuses.online' + (forTooltip ? '-tooltip' : '');
      } else if (node.health && node.health.servicesHealth === KnownHealthStatuses.Unhealthy) {
        return 'node.statuses.partially-online' + (forTooltip ? '-tooltip' : '');
      } else if (node.health && node.health.servicesHealth === KnownHealthStatuses.Connecting) {
        return 'node.statuses.connecting' + (forTooltip ? '-tooltip' : '');
      } else {
        return 'node.statuses.unknown' + (forTooltip ? '-tooltip' : '');
      }
    } else if (node.isStale) {
      return 'node.statuses.stale' + (forTooltip ? '-tooltip' : '');
    } else {
      return 'node.statuses.offline' + (forTooltip ? '-tooltip' : '');
    }
  }

  /**
   * Returns "last seen Xm ago" for a stale-cached row, or empty for
   * fresh / never-seen rows. Used as a small caption beside the row's
   * label so the operator knows how old the displayed data is.
   */
  lastSeenText(node: Node): string {
    if (!node.lastSeenAt) {
      return '';
    }
    const seen = new Date(node.lastSeenAt).getTime();
    if (isNaN(seen)) {
      return '';
    }
    const diffMs = Date.now() - seen;
    if (diffMs < 0) {
      return '';
    }
    const sec = Math.floor(diffMs / 1000);
    if (sec < 60) {
      return `last seen ${sec}s ago`;
    }
    const min = Math.floor(sec / 60);
    if (min < 60) {
      return `last seen ${min}m ago`;
    }
    const hr = Math.floor(min / 60);
    if (hr < 24) {
      return `last seen ${hr}h ago`;
    }
    const day = Math.floor(hr / 24);
    return `last seen ${day}d ago`;
  }

  /**
   * Makes the node list to be immediately refreshed.
   * @param requestedManually True if the data is going to be loaded because of a direct request
   * from the user.
   */
  forceDataRefresh(requestedManually = false) {
    if (requestedManually) {
      this.lastUpdateRequestedManually = true;
    }

    this.multipleNodeDataService.forceRefresh();
  }

  /**
   * Starts getting the data from the backend.
   */
  private startGettingData(checkSavedData: boolean) {
    // Use saved data or get from the server. If there is no saved data, savedData is null.
    const savedData = checkSavedData ? this.getLocalValue(this.persistentServerDataResponseKey) : null;
    let nextOperation: Observable<any> = this.multipleNodeDataService.startRequestingData();
    if (savedData) {
      nextOperation = of(JSON.parse(savedData.value));
    }

    // Get the node list.
    this.dataSubscription = nextOperation.subscribe((result: MultipleNodesBackendData) => {        
      if (!savedData) {
        this.saveLocalValue(this.persistentServerDataResponseKey, JSON.stringify(result));
      }

      this.updating = result ? result.updating : true;

      if (result && !result.updating) {
        // If the data was obtained.
        if (result.data && !result.error) {
          this.allNodes = result.data as Node[];
          this.dataFilterer.setData(this.allNodes);

          // Tree shape from #2633's /api/visors-tree-summary. Populates
          // `sections` for the next-step multi-table render. Header
          // shows the local hypervisor PK; if the response is empty
          // (older backend, or pre-init), fall back to whatever the
          // first node's PK is so the header isn't blank.
          if (result.sections && result.sections.length > 0) {
            this.sections = result.sections;
            this.localHypervisorPk = result.sections[0].hypervisorPk;
          } else {
            this.sections = [];
            this.localHypervisorPk = '';
          }

          // Fetch reward data if on the rewards tab and not yet loaded
          if (this.showRewardsInfo && !this.rewardDataLoaded && !this.rewardDataLoading) {
            this.loadRewardData();
          }

          this.loading = false;
          // Close any previous temporary loading error msg.
          this.snackbarService.closeCurrentIfTemporaryError();

          this.lastUpdate = result.momentOfLastCorrectUpdate;
          this.secondsSinceLastUpdate = Math.floor((Date.now() - result.momentOfLastCorrectUpdate) / 1000);
          this.errorsUpdating = false;
          AppComponent.currentInstance.hideDataProblemMsg();

          if (this.lastUpdateRequestedManually) {
            // Show a confirmation msg.
            this.snackbarService.showDone('common.refreshed', null);
            this.lastUpdateRequestedManually = false;
          }

        // If there was an error while obtaining the data.
        } else if (result.error) {
          // Show an error msg if it has not be done before during the current attempt to obtain the data.
          if (!this.errorsUpdating) {
            if (this.loading) {
              this.snackbarService.showError('common.loading-error', null, true, result.error);
            } else {
              this.snackbarService.showError('nodes.error-load', null, true, result.error);
            }
          }

          // Stop the loading indicator and show a warning icon.
          this.loading = false;
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

  /**
   * Recalculates which elements should be shown on the UI.
   */
  private recalculateElementsToShow() {
    // Needed to prevent racing conditions.
    this.currentPage = this.currentPageInUrl;

    // Needed to prevent racing conditions.
    if (this.filteredNodes) {
      // Annotate before pagination so the sort columns referencing
      // transportsCount / servicesCount have values to sort by.
      // Annotation is per-node and idempotent — safe to re-run on
      // every refresh.
      this.filteredNodes.forEach(n => this.annotateForSort(n));

      // If the user has dragged rows into a custom order, apply that
      // ordering first (over the column-sorter's output). Rows not in
      // the saved order keep the sorter's relative order, appended
      // after the explicit rows. Empty customNodeOrder = pure column
      // sort (the historical behavior).
      if (this.customNodeOrder.length > 0) {
        this.filteredNodes = this.applyCustomOrder(this.filteredNodes);
      }

      // Calculate the pagination values.
      const maxElements = AppConfig.maxFullListElements;
      this.numberOfPages = Math.ceil(this.filteredNodes.length / maxElements);
      if (this.currentPage > this.numberOfPages) {
        this.currentPage = this.numberOfPages;
      }

      // Limit the elements to show.
      const start = maxElements * (this.currentPage - 1);
      const end = start + maxElements;
      this.nodesToShow = this.filteredNodes.slice(start, end);
    } else {
      this.nodesToShow = null;
    }

    if (this.nodesToShow) {
      this.dataSource = this.nodesToShow;
    }
  }

  /**
   * Pre-computes the numeric counts the new column sorters need
   * (transports + services). Mutates the node in place. Safe to
   * re-run on each refresh — the values come from already-loaded
   * node fields, no extra API calls.
   */
  private annotateForSort(node: Node): void {
    node.transportsCount = node.transports ? node.transports.length : 0;
    node.servicesCount = this.getNodeServices(node).length;
  }

  /**
   * Reorders `nodes` so any PK present in customNodeOrder comes
   * first (in the saved order), followed by everything else in its
   * existing (column-sorter-imposed) order. Doesn't allocate when
   * customNodeOrder is empty.
   */
  private applyCustomOrder(nodes: Node[]): Node[] {
    if (this.customNodeOrder.length === 0) {
      return nodes;
    }
    const byPk = new Map<string, Node>();
    nodes.forEach(n => byPk.set(n.localPk, n));
    const ordered: Node[] = [];
    const seen = new Set<string>();
    for (const pk of this.customNodeOrder) {
      const n = byPk.get(pk);
      if (n) {
        ordered.push(n);
        seen.add(pk);
      }
    }
    nodes.forEach(n => {
      if (!seen.has(n.localPk)) {
        ordered.push(n);
      }
    });
    return ordered;
  }

  /**
   * cdkDropList drop handler. Reorders the visible page in place and
   * persists the resulting PK order to localStorage. The whole-list
   * order (across pagination) is reconstructed by capturing the
   * current filteredNodes PK order with the drop applied; rows on
   * other pages keep their existing slot.
   */
  rowDropped(event: CdkDragDrop<Node[]>): void {
    if (!this.nodesToShow || event.previousIndex === event.currentIndex) {
      return;
    }
    // Move within the visible page first so the UI updates without
    // waiting for a full refresh cycle.
    moveItemInArray(this.nodesToShow, event.previousIndex, event.currentIndex);
    this.dataSource = this.nodesToShow;

    // Reconstruct full filteredNodes order: keep the page's new
    // arrangement and merge with the other pages' rows in their
    // original order.
    const pageSize = AppConfig.maxFullListElements;
    const start = pageSize * (this.currentPage - 1);
    const newFiltered = this.filteredNodes.slice();
    for (let i = 0; i < this.nodesToShow.length; i++) {
      newFiltered[start + i] = this.nodesToShow[i];
    }
    this.filteredNodes = newFiltered;

    // Persist the entire filtered order as the custom order.
    this.customNodeOrder = newFiltered.map(n => n.localPk);
    this.saveLocalValue(this.customNodeOrderKey, JSON.stringify(this.customNodeOrder));
  }

  /**
   * Clears the user's drag-drop custom order. Triggered by the
   * column-sort headers (which would otherwise be ignored for any
   * rows the user manually arranged). Without this, clicking a
   * column header on a list where the user had dragged rows would
   * leave the rows stuck where they were, surprising operators
   * who expected a sort to fully take effect.
   */
  resetCustomOrder(): void {
    if (this.customNodeOrder.length === 0) {
      return;
    }
    this.customNodeOrder = [];
    this.saveLocalValue(this.customNodeOrderKey, '');
  }

  /**
   * Wrapper around dataSorter.changeSortingOrder that also clears
   * any active custom drag-drop order, then re-paginates so the
   * fresh sort takes effect immediately. Used by the column header
   * (click) handlers in node-list.component.html so a column sort
   * always wins over a previously-set custom order.
   */
  changeSort(column: SortingColumn): void {
    this.resetCustomOrder();
    this.dataSorter.changeSortingOrder(column);
  }

  logout() {
    const confirmationDialog = GeneralUtils.createConfirmationDialog(this.dialog, 'common.logout-confirmation');

    confirmationDialog.componentInstance.operationAccepted.subscribe(() => {
      confirmationDialog.componentInstance.closeModal();

      this.authService.logout().subscribe(
        () => this.router.navigate(['login']),
        () => this.snackbarService.showError('common.logout-error')
      );
    });
  }

  // Updates all visors.
  updateAll() {
    if (!this.dataSource || this.dataSource.length === 0) {
      this.snackbarService.showError('nodes.no-visors-to-update');

      return;
    }

    const updatableNodes: NodeData[] = [];
    const nonUpdatableNodes: NodeData[] = [];
    this.dataSource.forEach(node => {
      if (node.online) {
        const nodeData: NodeData = {
          key: node.localPk,
          label: node.label,
          version: node.version,
          tag: node.buildTag,
        };

        if (GeneralUtils.checkIfTagIsUpdatable(node.buildTag)) {
          updatableNodes.push(nodeData);
        } else {
          nonUpdatableNodes.push(nodeData);
        }
      }
    });

    UpdateAllComponent.openDialog(this.dialog, updatableNodes, nonUpdatableNodes);
  }

  // Opens the modal window for changing the rewards address of all online nodes.
  changeRewardsToAll() {
    if (!this.dataSource || this.dataSource.length === 0) {
      this.snackbarService.showError('nodes.no-visors-to-modify');

      return;
    }

    const nodesToModify: NodeToEditData[] = [];
    this.dataSource.forEach(node => {
      if (node.online) {
        const nodeData: NodeToEditData = {
          key: node.localPk,
          label: node.label,
        };
        nodesToModify.push(nodeData);
      }
    });

    const params: BulkRewardAddressParams = { nodes: nodesToModify };

    BulkRewardAddressChangerComponent.openDialog(this.dialog, params);
  }

  /**
   * Recursively updates the visors in the list. It returns how many visors the function was not
   * able to update.
   * @param keys Keys of the visors to update. The list will be altered by the function.
   * @param labels Labels of the visors to update. The list will be altered by the function.
   * @param errors Errors found during the process. For internal use.
   */
  private recursivelyUpdateWallets(keys: string[], labels: string[], errors = 0): Observable<number> {
    /* eslint-disable arrow-body-style */
    return this.nodeService.update(keys[keys.length - 1]).pipe(catchError(() => {
      // If there is a problem updating a visor, return null to be able to continue with
      // the process.
      return of(null);
    }), mergeMap(response => {
      // Show the result of the current step.
      if (response && response.updated && !response.error) {
        this.snackbarService.showDone(this.translateService.instant('nodes.update.done', { name: labels[labels.length - 1] }));
      } else {
        this.snackbarService.showError(this.translateService.instant('nodes.update.update-error', { name: labels[labels.length - 1] }));
        errors += 1;
      }

      keys.pop();
      labels.pop();

      // Go to the next step.
      if (keys.length >= 1) {
        return this.recursivelyUpdateWallets(keys, labels, errors);
      }

      return of(errors);
    }));
  }

  /**
   * Opens the modal window used on small screens with the options of an element.
   */
  showOptionsDialog(node: Node) {
    const options: SelectableOption[] = [
      {
        icon: 'filter_none',
        label: 'nodes.copy-key',
      }
    ];

    options.push({
      icon: 'short_text',
      label: 'labeled-element.edit-label',
    });

    if (!node.online) {
      options.push({
        icon: 'close',
        label: 'nodes.delete-node',
      });
    }

    SelectOptionComponent.openDialog(this.dialog, options, 'common.options').afterClosed().subscribe((selectedOption: number) => {
      if (selectedOption === 1) {
        this.copySpecificTextToClipboard(node.localPk);
      } else if (selectedOption === 2) {
        this.showEditLabelDialog(node);
      } else if (selectedOption === 3) {
        this.deleteNode(node);
      }
    });
  }

  /**
   * Copies the public key of a visor to the clipboard.
   */
  copyToClipboard(node: Node) {
    this.copySpecificTextToClipboard(node.localPk);
  }

  /**
   * Copies a text to the clipboard.
   * @param text Text to copy.
   */
  private copySpecificTextToClipboard(text: string) {
    if (this.clipboardService.copy(text)) {
      this.snackbarService.showDone('copy.copied');
    }
  }

  /**
   * Opens the modal window for changing the label of a node.
   */
  showEditLabelDialog(node: Node) {
    let labelInfo =  this.storageService.getLabelInfo(node.localPk);
    if (!labelInfo) {
      labelInfo = {
        id: node.localPk,
        label: '',
        identifiedElementType: LabeledElementTypes.Node,
      };
    }

    EditLabelComponent.openDialog(this.dialog, labelInfo).afterClosed().subscribe((changed: boolean) => {
      if (changed) {
        this.forceDataRefresh();
      }
    });
  }

  /**
   * Removes an offline node from the list, until seeing it online again.
   */
  deleteNode(node: Node) {
    const confirmationDialog = GeneralUtils.createConfirmationDialog(this.dialog, 'nodes.delete-node-confirmation');

    confirmationDialog.componentInstance.operationAccepted.subscribe(() => {
      confirmationDialog.close();
      this.storageService.setLocalNodesAsHidden([node.localPk], [node.ip]);
      this.forceDataRefresh();
      this.snackbarService.showDone('nodes.deleted');
    });
  }

  /**
   * Returns an array of {type, count} entries for transport types on the given node.
   * Types are displayed uppercase.
   */
  getTransportCounts(node: Node): {type: string, count: number}[] {
    if (!node.transports || node.transports.length === 0) {
      return [];
    }
    const counts: {[key: string]: number} = {};
    node.transports.forEach(t => {
      const tp = (t.type || 'unknown').toUpperCase();
      // Skip the "?" placeholder type emitted by sub-hypervisors that
      // predate the TransportSummaries field (#2789) — they only send
      // the count and the backend synthesizes typeless placeholders to
      // keep node.transports.length right. Including "?" as a per-type
      // row clutters the cell ("?: 35"). The template still renders
      // the Total line from node.transports.length so the operator
      // sees the count.
      if (tp === '?') {
        return;
      }
      counts[tp] = (counts[tp] || 0) + 1;
    });
    return Object.keys(counts).sort().map(k => ({type: k, count: counts[k]}));
  }

  /**
   * Returns an array of service display names active on the given node.
   */
  getNodeServices(node: Node): string[] {
    const services: string[] = [];
    if (node.apps) {
      node.apps.forEach(app => {
        if (app.status === 1) {
          if (app.name === 'skysocks') {
            services.push('Proxy Server');
          } else if (app.name === 'vpn-server') {
            services.push('VPN Server');
          }
        }
      });
    }
    if (node.isPublic) {
      services.push('Public Visor');
    }
    if (node.autoconnectTransports) {
      services.push('Autoconnect');
    }
    return services;
  }

  /**
   * Loads reward data for all known visors.
   */
  private loadRewardData(): void {
    if (!this.allNodes || this.allNodes.length === 0) {
      return;
    }

    this.rewardDataLoading = true;
    const pks = this.allNodes.map(n => n.localPk);

    this.rewardService.fetchRewardData(pks).subscribe(dataMap => {
      this.rewardDataMap = dataMap;
      this.rewardDates = this.rewardService.getCachedDates();
      this.rewardDateHeaders = this.rewardDates.map(d => this.rewardService.formatDateShort(d));
      this.rewardDataLoading = false;
      this.rewardDataLoaded = true;
    });
  }

  /**
   * Returns the reward data for a given visor PK, or null if not available.
   */
  getRewardData(pk: string): VisorRewardData | null {
    return this.rewardDataMap.get(pk) || null;
  }

  /**
   * Returns the reward amount for a visor on a given date, formatted for display.
   */
  getRewardAmount(pk: string, date: string): string {
    const data = this.rewardDataMap.get(pk);
    if (!data || !data.dailyAmounts[date] || data.dailyAmounts[date] === 0) {
      return '-';
    }
    return data.dailyAmounts[date].toFixed(2);
  }

  /**
   * Returns the CSS class for a reward cell based on sent status.
   */
  getRewardClass(pk: string, date: string): string {
    const data = this.rewardDataMap.get(pk);
    if (!data || !data.dailyAmounts[date] || data.dailyAmounts[date] === 0) {
      return '';
    }
    return data.dailySent[date] ? 'reward-sent' : 'reward-pending';
  }

  /**
   * Returns the week total for a visor, formatted for display.
   */
  getWeekTotal(pk: string): string {
    const data = this.rewardDataMap.get(pk);
    if (!data || data.weekTotal === 0) {
      return '-';
    }
    return data.weekTotal.toFixed(2);
  }

  /**
   * Returns the reward address for a visor, or "-" if not set.
   */
  getRewardAddress(pk: string): string {
    // First check the node's own rewardsAddress field
    const node = this.allNodes?.find(n => n.localPk === pk);
    if (node?.rewardsAddress) {
      return node.rewardsAddress;
    }
    // Fall back to reward service data
    const data = this.rewardDataMap.get(pk);
    if (data?.rewardAddress) {
      return data.rewardAddress;
    }
    return '-';
  }

  /**
   * Removes all offline nodes from the list, until seeing them online again.
   */
  removeOffline() {
    let confirmationText = 'nodes.delete-all-offline-confirmation';
    if (this.dataFilterer.currentFiltersTexts && this.dataFilterer.currentFiltersTexts.length > 0) {
      confirmationText = 'nodes.delete-all-filtered-offline-confirmation';
    }

    const confirmationDialog = GeneralUtils.createConfirmationDialog(this.dialog, confirmationText);

    confirmationDialog.componentInstance.operationAccepted.subscribe(() => {
      confirmationDialog.close();

      // Prepare all offline nodes to be removed.
      const nodesToRemove: string[] = [];
      const ipsToRemove: string[] = [];
      this.filteredNodes.forEach(node => {
        if (!node.online) {
          nodesToRemove.push(node.localPk);
          ipsToRemove.push(node.ip);
        }
      });

      // Remove the nodes and show the result.
      if (nodesToRemove.length > 0) {
        this.storageService.setLocalNodesAsHidden(nodesToRemove, ipsToRemove);

        this.forceDataRefresh();

        if (nodesToRemove.length === 1) {
          this.snackbarService.showDone('nodes.deleted-singular');
        } else {
          this.snackbarService.showDone('nodes.deleted-plural', { number: nodesToRemove.length });
        }
      }
    });
  }
}
