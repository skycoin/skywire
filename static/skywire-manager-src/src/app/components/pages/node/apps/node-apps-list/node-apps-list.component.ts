import { Component, Input, OnDestroy, OnInit } from '@angular/core';
import { MatDialog, MatDialogRef } from '@angular/material/dialog';
import { Observable, Subscription, map } from 'rxjs';
import { ActivatedRoute, Router } from '@angular/router';
import { TranslateService } from '@ngx-translate/core';
import { StorageService } from 'src/app/services/storage.service';

import { Application } from '../../../../../app.datatypes';
import { AppsService } from '../../../../../services/apps.service';
import { LogComponent } from './log/log.component';
import { NodeComponent } from '../../node.component';
import { AppConfig } from '../../../../../app.config';
import GeneralUtils from '../../../../../utils/generalUtils';
import { ConfirmationComponent } from '../../../../layout/confirmation/confirmation.component';
import { SnackbarService } from '../../../../../services/snackbar.service';
import { SelectableOption, SelectOptionComponent } from 'src/app/components/layout/select-option/select-option.component';
import { processServiceError } from 'src/app/utils/errors';
import { OperationError } from 'src/app/utils/operation-error';
import { FilterProperties, FilterFieldTypes } from 'src/app/utils/filters';
import { SortingColumn, SortingModes, DataSorter } from 'src/app/utils/lists/data-sorter';
import { DataFilterer } from 'src/app/utils/lists/data-filterer';

/**
 * Shows the list of applications of a node. It shows official or user apps, not both at the
 * same time. I can be used to show a short preview, with just some elements and a link for
 * showing the rest, or the full list, with pagination controls. When adding to a component,
 * the showOfficialApps property should be set first.
 */
@Component({
    selector: 'app-node-app-list',
    templateUrl: './node-apps-list.component.html',
    styleUrls: ['./node-apps-list.component.scss'],
    standalone: false
})
export class NodeAppsListComponent implements OnInit, OnDestroy {
  // Small text for identifying the list, needed for the helper objects.
  private readonly listIdForOfficialApps = 'op';
  private readonly listIdForUserApps = 'up';

  // Set with the names of the official apps.
  // Official apps — those shipped with the visor binary and emitted
  // by config-gen. Used to split the apps list between "Official" and
  // "User" tabs and to gate the per-app settings dialog dispatch.
  // Multi-instance entries (skysocks-client-N, skycoin-daemon-aix,
  // etc.) match by prefix in isOfficialApp() below.
  private readonly officialAppsList = new Set<string>([
    'skychat', 'skysocks', 'skysocks-client', 'vpn-client', 'vpn-server',
    'skycoin-daemon', 'skycoin-web',
  ]);

  // isOfficialApp covers the multi-instance naming scheme:
  // 'skysocks-client-2' → skysocks-client family,
  // 'skycoin-daemon-aix' → skycoin-daemon family. Multi-instance
  // entries should land under "Official" alongside their canonical
  // sibling, not in "User".
  private isOfficialApp(name: string): boolean {
    if (this.officialAppsList.has(name)) {
      return true;
    }
    for (const base of this.officialAppsList) {
      if (name.startsWith(base + '-')) {
        return true;
      }
    }
    return false;
  }

  @Input() nodePK: string;
  @Input() nodeIp: string;
  @Input() showOfficialApps = true;

  // Vars with the data of the columns used for sorting the data.
  stateSortData = new SortingColumn(['status'], 'apps.apps-list.state', SortingModes.NumberReversed);
  nameSortData = new SortingColumn(['name'], 'apps.apps-list.app-name', SortingModes.Text);
  portSortData = new SortingColumn(['port'], 'apps.apps-list.port', SortingModes.Number);
  autoStartSortData = new SortingColumn(['autostart'], 'apps.apps-list.auto-start', SortingModes.Boolean);

  private dataSortedSubscription: Subscription;
  private dataFiltererSubscription: Subscription;
  // Objects in charge of sorting and filtering the data.
  dataSorter: DataSorter;
  dataFilterer: DataFilterer;

  dataSource: Application[];
  /**
   * Keeps track of the state of the check boxes of the elements.
   */
  selections = new Map<string, boolean>();

  /**
   * If true, the control can only show few elements and, if there are more elements, a link for
   * accessing the full list. If false, the full list is shown, with pagination
   * controls, if needed.
   */
  showShortList_: boolean;
  @Input() set showShortList(val: boolean) {
    this.showShortList_ = val;
    // Sort the data.
    if (this.dataSorter) {
      this.dataSorter.setData(this.filteredApps);
    }
  }

  // List with the names of all the apps which can not be configured directly on the manager.
  appsWithoutConfig = new Set<string>();

  // All apps the ode has.
  allApps: Application[];
  // All apps the node has for the selected type (official apps or user apps).
  allAppsForType: Application[];
  filteredApps: Application[];
  appsToShow: Application[];
  appsMap: Map<string, Application>;
  numberOfPages = 1;
  currentPage = 1;
  // Used as a helper var, as the URL is readed asynchronously.
  currentPageInUrl = 1;
  @Input() set apps(val: Application[]) {
    this.allApps = val ? val : [];

    // Use only the apps for the selected type.
    if (this.allApps) {
      this.allAppsForType = [];
      this.allApps.forEach(app => {
        if (this.showOfficialApps && this.isOfficialApp(app.name)) {
          this.allAppsForType.push(app);
        } else if (!this.showOfficialApps && !this.isOfficialApp(app.name)) {
          this.allAppsForType.push(app);
        }
      });

      if (this.dataFilterer) {
        this.dataFilterer.setData(this.allAppsForType);
      }
    }
  }

  // Array with the properties of the columns that can be used for filtering the data.
  filterProperties: FilterProperties[] = [
    {
      filterName: 'apps.apps-list.filter-dialog.state',
      keyNameInElementsArray: 'status',
      type: FilterFieldTypes.Select,
      printableLabelsForValues: [
        {
          value: '',
          label: 'apps.apps-list.filter-dialog.state-options.any',
        },
        {
          value: '1',
          label: 'apps.apps-list.filter-dialog.state-options.running',
        },
        {
          value: '0',
          label: 'apps.apps-list.filter-dialog.state-options.stopped',
        }
      ],
    },
    {
      filterName: 'apps.apps-list.filter-dialog.name',
      keyNameInElementsArray: 'name',
      type: FilterFieldTypes.TextInput,
      maxlength: 50,
    },
    {
      filterName: 'apps.apps-list.filter-dialog.port',
      keyNameInElementsArray: 'port',
      type: FilterFieldTypes.TextInput,
      maxlength: 8,
    },
    {
      filterName: 'apps.apps-list.filter-dialog.autostart',
      keyNameInElementsArray: 'autostart',
      type: FilterFieldTypes.Select,
      printableLabelsForValues: [
        {
          value: '',
          label: 'apps.apps-list.filter-dialog.autostart-options.any',
        },
        {
          value: 'true',
          label: 'apps.apps-list.filter-dialog.autostart-options.enabled',
        },
        {
          value: 'false',
          label: 'apps.apps-list.filter-dialog.autostart-options.disabled',
        }
      ],
    },
  ];

  /**
   * Indicates that, after updating the data, it has to be updated again after a small delay.
   */
  private refreshAgain = false;

  private navigationsSubscription: Subscription;
  private operationSubscriptionsGroup: Subscription[] = [];

  constructor(
    private appsService: AppsService,
    private dialog: MatDialog,
    private route: ActivatedRoute,
    private router: Router,
    private snackbarService: SnackbarService,
    private translateService: TranslateService,
    private storageService: StorageService,
  ) {
    // Get the page and type requested in the URL.
    this.navigationsSubscription = this.route.paramMap.subscribe(params => {
      if (params.has('showOfficialApps')) {
        this.showOfficialApps = params.get('showOfficialApps').toUpperCase() === 'true'.toUpperCase();
      }

      if (params.has('page')) {
        let selectedPage = Number.parseInt(params.get('page'), 10);
        if (isNaN(selectedPage) || selectedPage < 1) {
          selectedPage = 1;
        }

        this.currentPageInUrl = selectedPage;

        this.recalculateElementsToShow();
      }
    });
  }

  ngOnInit() {
    const listIdToUse = this.showOfficialApps ? this.listIdForOfficialApps : this.listIdForUserApps;

    // Initialize the data sorter.
    const sortableColumns: SortingColumn[] = [
      this.stateSortData,
      this.nameSortData,
      this.portSortData,
      this.autoStartSortData,
    ];
    this.dataSorter = new DataSorter(this.dialog, this.translateService, this.storageService, sortableColumns, 1, listIdToUse);
    this.dataSortedSubscription = this.dataSorter.dataSorted.subscribe(() => {
      // When this happens, the data in allAppsForType has already been sorted.
      this.recalculateElementsToShow();
    });

    if (this.dataSorter) {
      this.dataSorter.setData(this.filteredApps);
    }

    this.dataFilterer = new DataFilterer(this.dialog, this.route, this.router, this.filterProperties, listIdToUse);
    this.dataFiltererSubscription = this.dataFilterer.dataFiltered.subscribe(data => {
      this.filteredApps = data;
      this.dataSorter.setData(this.filteredApps);
    });

    if (this.allAppsForType) {
      this.dataFilterer.setData(this.allAppsForType);
    }
  }

  ngOnDestroy() {
    this.navigationsSubscription.unsubscribe();
    this.operationSubscriptionsGroup.forEach(sub => sub.unsubscribe());
    this.dataSortedSubscription.unsubscribe();
    this.dataFiltererSubscription.unsubscribe();
    this.dataSorter.dispose();
    this.dataFilterer.dispose();
  }

  /**
   * Gets the link for openning the UI of an app. Currently only works for the Skychat app.
   */
  getLink(app: Application): string {
    if (app.name.toLocaleLowerCase() === 'skychat' && this.nodeIp && app.status !== 0 && app.status !== 2) {
      // Default port and ip.
      let port = '8001';
      let url = '127.0.0.1';

      // Try to get the address and port from the config array.
      if (app.args) {
        for (let i = 0; i < app.args.length; i++) {
          if (app.args[i] === '-addr' && i + 1 < app.args.length) {
            const addr = (app.args[i + 1] as string).trim();

            const parts = addr.split(':');
            // If the app can be accessed outside localhost, use the remote ip.
            if (parts[0] === '*') {
              url = this.nodeIp;
            }

            port = parts[1];
          }
        }
      }

      if (!port.startsWith(':')) {
        port = ':' + port;
      }

      return 'http://' + url + port;
    } else if (app.name.toLocaleLowerCase() === 'vpn-client' && this.nodePK) {
      return location.origin + '/#/vpn/' + this.nodePK + '/status';
    } else if (!this.isOfficialApp(app.name)) {
      // Try to get the URL arg. If found, return the URL.
      if (app.args) {
        const urlArgsSet = new Set<string>(['url', '-url']);
        let url: string = null;

        for (let i = 0; i < app.args.length; i++) {
          if (urlArgsSet.has((app.args[i] as string).toLowerCase()) && i + 1 < app.args.length) {
            url = (app.args[i + 1] as string).trim();
            break;
          }
        }

        return url;
      }
    }

    return null;
  }

  /**
   * Gets the class for the app status dot.
   */
  getStateClass(app: Application): string {
    if (app.status === 1) {
      return 'dot-green';
    } else if (app.status === 3) {
      return 'dot-yellow';
    }

    return 'dot-red';
  }

  /**
   * Gets the class for the app status text in small screens.
   */
   getSmallScreenStateClass(app: Application): string {
    if (app.status === 1) {
      return 'green-clear-text';
    } else if (app.status === 3) {
      return 'yellow-clear-text';
    }

    return 'red-clear-text';
  }

  /**
   * Gets the tooltip text for the app status dot.
   */
  getStateTooltip(app: Application): string {
    if (app.status === 1) {
      return 'apps.status-running-tooltip';
    } else if (app.status === 3) {
      return 'apps.status-connecting-tooltip';
    }

    return 'apps.status-stopped-tooltip';
  }

  /**
   * Gets the text for status shown in small screens.
   */
   getSmallScreenStateTextVar(app: Application): string {
    if (app.status === 1) {
      return 'apps.status-running';
    } else if (app.status === 2) {
      return 'apps.status-failed';
    } else if (app.status === 3) {
      return 'apps.status-connecting';
    }

    return 'apps.status-stopped';
  }

  /**
   * Changes the selection state of an entry (modifies the state of its checkbox).
   */
  changeSelection(app: Application) {
    if (this.selections.get(app.name)) {
      this.selections.set(app.name, false);
    } else {
      this.selections.set(app.name, true);
    }
  }

  /**
   * Check if at least one entry has been selected via its checkbox.
   */
  hasSelectedElements(): boolean {
    if (!this.selections) {
      return false;
    }

    let found = false;
    this.selections.forEach((val) => {
      if (val) {
        found = true;
      }
    });

    return found;
  }

  /**
   * Selects or deselects all items.
   */
  changeAllSelections(setSelected: boolean) {
    this.selections.forEach((val, key) => {
      this.selections.set(key, setSelected);
    });
  }

  /**
   * Starts or stops the selected apps.
   */
  changeStateOfSelected(startApps: boolean) {
    const elementsToChange: string[] = [];
    // Ignore all elements which already have the desired settings applied.
    this.selections.forEach((val, key) => {
      if (val) {
        if (
          (startApps && (this.appsMap.get(key).status === 0 || this.appsMap.get(key).status === 2)) ||
          (!startApps && (this.appsMap.get(key).status !== 0 && this.appsMap.get(key).status !== 2))
        ) {
          elementsToChange.push(key);
        }
      }
    });

    // No confirmation modal — start/stop are reversible (start the
    // app again to undo a stop) and the snackbar reports completion.
    this.changeAppsValRecursively(elementsToChange, false, startApps);
  }

  /**
   * Changes the autostart setting of the selected apps.
   */
  changeAutostartOfSelected(autostart: boolean) {
    const elementsToChange: string[] = [];
    // Ignore all elements which already have the desired settings applied.
    this.selections.forEach((val, key) => {
      if (val) {
        if ((autostart && !this.appsMap.get(key).autostart) || (!autostart && this.appsMap.get(key).autostart)) {
          elementsToChange.push(key);
        }
      }
    });
    if (elementsToChange.length === 0) {
      return;
    }

    // No confirmation modal — autostart is reversible and the
    // snackbar confirms each completed batch.
    this.changeAppsValRecursively(elementsToChange, true, autostart, null);
  }

  /**
   * Opens the modal window used on small screens with the options of an element.
   */
  showOptionsDialog(app: Application) {
    const options: SelectableOption[] = [
      {
        icon: 'list',
        label: 'apps.view-logs',
      },
      {
        icon: app.status === 0 || app.status === 2 ? 'play_arrow' : 'stop',
        label: 'apps.' + (app.status === 0 || app.status === 2 ? 'start-app' : 'stop-app'),
      },
      {
        icon: app.autostart ? 'close' : 'done',
        label: app.autostart ? 'apps.apps-list.disable-autostart' : 'apps.apps-list.enable-autostart',
      }
    ];

    if (!this.appsWithoutConfig.has(app.name)) {
      options.push({
        icon: 'settings',
        label: 'apps.settings',
      });
    }

    SelectOptionComponent.openDialog(this.dialog, options, 'common.options').afterClosed().subscribe((selectedOption: number) => {
      if (selectedOption === 1) {
        this.viewLogs(app);
      } else if (selectedOption === 2) {
        this.changeAppState(app);
      } else if (selectedOption === 3) {
        this.changeAppAutostart(app);
      } else if (selectedOption === 4) {
        this.toggleExpanded(app.name);
      }
    });
  }

  /**
   * Starts or stops a specific app. No confirmation modal —
   * reversible by starting/stopping again; snackbar reports it.
   */
  changeAppState(app: Application): void {
    const wantStart = (app.status === 0 || app.status === 2);
    this.changeSingleAppVal(
      this.startChangingAppState(app.name, wantStart),
    );
  }

  /**
   * Changes the autostart setting of a specific app. Reversible —
   * flip + snackbar, no modal.
   */
  changeAppAutostart(app: Application): void {
    this.changeSingleAppVal(
      this.startChangingAppAutostart(app.name, !app.autostart),
    );
  }

  /**
   * Helper function used for starting a process for changing a value on an app and reacting to the result.
   * Used to avoid repeating common code.
   * @param observable Observable which will start the operation after subscription.
   * @param confirmationDialog Dialog used for requesting confirmation from the user.
   */
  private changeSingleAppVal(
    observable: Observable<any>,
    confirmationDialog: MatDialogRef<ConfirmationComponent, any> = null) {

    // Start the operation and save it for posible cancellation.
    this.operationSubscriptionsGroup.push(observable.subscribe(
      () => {
        if (confirmationDialog) {
          confirmationDialog.close();
        }

        // Make the parent page reload the data and do it again after a small delay, to catch
        // slow changes.
        setTimeout(() => {
          this.refreshAgain = true;
          NodeComponent.refreshCurrentDisplayedData();
        }, 50);
        this.snackbarService.showDone('apps.operation-completed');
      }, (err: OperationError) => {
        err = processServiceError(err);

        // Make the parent page reload the data and do it again after a small delay, to catch
        // slow changes.
        setTimeout(() => {
          this.refreshAgain = true;
          NodeComponent.refreshCurrentDisplayedData();
        }, 50);

        if (confirmationDialog) {
          confirmationDialog.componentInstance.showDone('confirmation.error-header-text', err.translatableErrorMsg);
        } else {
          this.snackbarService.showError(err);
        }
      }
    ));
  }

  /**
   * Shows a modal window with the logs of an app.
   */
  viewLogs(app: Application): void {
    if (app.status !== 0 && app.status !== 2) {
      LogComponent.openDialog(this.dialog, app);
    } else {
      this.snackbarService.showError('apps.apps-list.unavailable-logs-error');
    }
  }

  // Names of apps whose universal settings panel is currently
  // expanded. Multiple panels can be open at once — they're inline
  // so they don't fight for focus the way the old dialogs did.
  expandedApps = new Set<string>();

  toggleExpanded(name: string): void {
    if (this.expandedApps.has(name)) {
      this.expandedApps.delete(name);
    } else {
      this.expandedApps.add(name);
    }
  }

  isExpanded(name: string): boolean {
    return this.expandedApps.has(name);
  }

  onSettingsSaved(name: string): void {
    this.expandedApps.delete(name);
  }

  /**
   * Recalculates which elements should be shown on the UI.
   */
  private recalculateElementsToShow() {
    // Needed to prevent racing conditions.
    this.currentPage = this.currentPageInUrl;

    // Needed to prevent racing conditions.
    if (this.filteredApps) {
      // Calculate the pagination values.
      const maxElements = this.showShortList_ ? AppConfig.maxShortListElements : AppConfig.maxFullListElements;
      this.numberOfPages = Math.ceil(this.filteredApps.length / maxElements);
      if (this.currentPage > this.numberOfPages) {
        this.currentPage = this.numberOfPages;
      }

      // Limit the elements to show.
      const start = maxElements * (this.currentPage - 1);
      const end = start + maxElements;
      this.appsToShow = this.filteredApps.slice(start, end);

      // Create a map with the elements to show, as a helper.
      this.appsMap = new Map<string, Application>();
      this.appsToShow.forEach(app => {
        this.appsMap.set(app.name, app);

        // Add to the selections map the elements that are going to be shown.
        if (!this.selections.has(app.name)) {
          this.selections.set(app.name, false);
        }
      });

      // Remove from the selections map the elements that are not going to be shown.
      const keysToRemove: string[] = [];
      this.selections.forEach((value, key) => {
        if (!this.appsMap.has(key)) {
          keysToRemove.push(key);
        }
      });
      keysToRemove.forEach(key => {
        this.selections.delete(key);
      });
    } else {
      this.appsToShow = null;
      this.selections = new Map<string, boolean>();
    }

    this.dataSource = this.appsToShow;

    // Refresh the data again after a small delay, if requested.
    if (this.refreshAgain) {
      this.refreshAgain = false;

      setTimeout(() => NodeComponent.refreshCurrentDisplayedData(), 2000);
    }
  }

  /**
   * Prepares the operation for starting or stopping an app, but does not start it. To start the operation,
   * subscribe to the response.
   */
  private startChangingAppState(appName: string, startApp: boolean): Observable<any> {
    return this.appsService.changeAppState(NodeComponent.getCurrentNodeKey(), appName, startApp).pipe(map(response => {
      if (response.status !== null && response.status !== undefined) {
        this.dataSource.forEach(app => {
          if (app.name === appName) {
            app.status = response.status;
            app.detailedStatus = response.detailed_status;
          }
        });
      }

      return response;
    }));
  }

  /**
   * Prepares the operation for changing the autostart setting of an app, but does not start it. To
   * start the operation, subscribe to the response.
   */
  private startChangingAppAutostart(appName: string, autostart: boolean): Observable<any> {
    return this.appsService.changeAppAutostart(NodeComponent.getCurrentNodeKey(), appName, autostart);
  }

  /**
   * Recursively changes a setting in a list of apps.
   * @param names List with the names of the apps to modify.
   * @param changingAutostart True if going to change the autostart setting, false if going to change
   * the running state of the apps.
   * @param newVal If "changingAutostart" is true, the new state of the autostart setting; otherwise,
   * true for starting the apps or false for stopping them.
   * @param confirmationDialog Dialog used for requesting confirmation from the user.
   */
  private changeAppsValRecursively(
    names: string[],
    changingAutostart: boolean,
    newVal: boolean,
    confirmationDialog: MatDialogRef<ConfirmationComponent, any> = null) {

    // The list may be empty because apps which already have the settings are ignored.
    if (!names || names.length === 0) {
      setTimeout(() => NodeComponent.refreshCurrentDisplayedData(), 50);
      this.snackbarService.showWarning('apps.operation-unnecessary');

      if (confirmationDialog) {
        confirmationDialog.close();
      }

      return;
    }

    let observable: Observable<any>;
    if (changingAutostart) {
      observable = this.startChangingAppAutostart(names[names.length - 1], newVal);
    } else {
      observable = this.startChangingAppState(names[names.length - 1], newVal);
    }

    this.operationSubscriptionsGroup.push(observable.subscribe(() => {
      names.pop();
      if (names.length === 0) {
        if (confirmationDialog) {
          confirmationDialog.close();
        }

        // Make the parent page reload the data and do it again after a small delay, to catch
        // slow changes.
        setTimeout(() => {
          this.refreshAgain = true;
          NodeComponent.refreshCurrentDisplayedData();
        }, 50);

        this.snackbarService.showDone('apps.operation-completed');
      } else {
        this.changeAppsValRecursively(names, changingAutostart, newVal, confirmationDialog);
      }
    }, (err: OperationError) => {
      err = processServiceError(err);

      // Make the parent page reload the data and do it again after a small delay, to catch
      // slow changes.
      setTimeout(() => {
        this.refreshAgain = true;
        NodeComponent.refreshCurrentDisplayedData();
      }, 50);

      if (confirmationDialog) {
        confirmationDialog.componentInstance.showDone('confirmation.error-header-text', err.translatableErrorMsg);
      } else {
        this.snackbarService.showError(err);
      }
    }));
  }
}
