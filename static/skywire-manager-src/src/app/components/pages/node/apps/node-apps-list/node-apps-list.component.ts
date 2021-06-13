import { Component, Input, OnDestroy } from '@angular/core';
import { MatDialog, MatDialogRef } from '@angular/material/dialog';
import { Observable, Subscription } from 'rxjs';
import { ActivatedRoute, Router } from '@angular/router';
import { TranslateService } from '@ngx-translate/core';

import { Application } from '../../../../../app.datatypes';
import { AppsService } from '../../../../../services/apps.service';
import { LogComponent } from './log/log.component';
import { NodeComponent } from '../../node.component';
import { AppConfig } from '../../../../../app.config';
import GeneralUtils from '../../../../../utils/generalUtils';
import { ConfirmationComponent } from '../../../../layout/confirmation/confirmation.component';
import { SnackbarService } from '../../../../../services/snackbar.service';
import { SelectableOption, SelectOptionComponent } from 'src/app/components/layout/select-option/select-option.component';
import { SkysocksSettingsComponent } from '../node-apps/skysocks-settings/skysocks-settings.component';
import { processServiceError } from 'src/app/utils/errors';
import { OperationError } from 'src/app/utils/operation-error';
import { SkysocksClientSettingsComponent } from '../node-apps/skysocks-client-settings/skysocks-client-settings.component';
import { FilterProperties, FilterFieldTypes } from 'src/app/utils/filters';
import { SortingColumn, SortingModes, DataSorter } from 'src/app/utils/lists/data-sorter';
import { DataFilterer } from 'src/app/utils/lists/data-filterer';

/**
 * Shows the list of applications of a node. I can be used to show a short preview, with just some
 * elements and a link for showing the rest: or the full list, with pagination controls.
 */
@Component({
  selector: 'app-node-app-list',
  templateUrl: './node-apps-list.component.html',
  styleUrls: ['./node-apps-list.component.scss']
})
export class NodeAppsListComponent implements OnDestroy {
  // Small text for identifying the list, needed for the helper objects.
  private readonly listId = 'ap';

  @Input() nodePK: string;
  @Input() nodeIp: string;

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
    this.dataSorter.setData(this.filteredApps);
  }

  // List with the names of all the apps which can be configured directly on the manager.
  appsWithConfig = new Map<string, boolean>([
    ['skysocks', true],
    ['skysocks-client', true],
    ['vpn-client', true],
    ['vpn-server', true],
  ]);

  allApps: Application[];
  filteredApps: Application[];
  appsToShow: Application[];
  appsMap: Map<string, Application>;
  numberOfPages = 1;
  currentPage = 1;
  // Used as a helper var, as the URL is read asynchronously.
  currentPageInUrl = 1;
  @Input() set apps(val: Application[]) {
    this.allApps = val ? val : [];

    this.dataFilterer.setData(this.allApps);
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
  ) {
    // Initialize the data sorter.
    const sortableColumns: SortingColumn[] = [
      this.stateSortData,
      this.nameSortData,
      this.portSortData,
      this.autoStartSortData,
    ];
    this.dataSorter = new DataSorter(this.dialog, this.translateService, sortableColumns, 1, this.listId);
    this.dataSortedSubscription = this.dataSorter.dataSorted.subscribe(() => {
      // When this happens, the data in allApps has already been sorted.
      this.recalculateElementsToShow();
    });

    this.dataFilterer = new DataFilterer(this.dialog, this.route, this.router, this.filterProperties, this.listId);
    this.dataFiltererSubscription = this.dataFilterer.dataFiltered.subscribe(data => {
      this.filteredApps = data;
      this.dataSorter.setData(this.filteredApps);
    });

    // Get the page requested in the URL.
    this.navigationsSubscription = this.route.paramMap.subscribe(params => {
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
    if (app.name.toLocaleLowerCase() === 'skychat' && this.nodeIp) {
      // Default port.
      let port = '8001';

      // Try to get the port from the config array.
      if (app.args) {
        for (let i = 0; i < app.args.length; i++) {
          if (app.args[i] === '-addr' && i + 1 < app.args.length) {
            port = (app.args[i + 1] as string).trim();
          }
        }
      }

      if (!port.startsWith(':')) {
        port = ':' + port;
      }

      return 'http://' + this.nodeIp + port;
    }

    return null;
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
   * Check if at lest one entry has been selected via its checkbox.
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
        if ((startApps && this.appsMap.get(key).status !== 1) || (!startApps && this.appsMap.get(key).status === 1)) {
          elementsToChange.push(key);
        }
      }
    });

    if (startApps) {
      this.changeAppsValRecursively(elementsToChange, false, startApps);
    } else {
      // Ask for confirmation if the apps are going to be stopped.
      const confirmationDialog = GeneralUtils.createConfirmationDialog(this.dialog, 'apps.stop-selected-confirmation');

      confirmationDialog.componentInstance.operationAccepted.subscribe(() => {
        confirmationDialog.componentInstance.showProcessing();

        this.changeAppsValRecursively(elementsToChange, false, startApps, confirmationDialog);
      });
    }
  }

  /**
   * Changes the autostart setting of the selected apps.
   */
  changeAutostartOfSelected(autostart: boolean) {
    const elementsToChange: string[] = [];
    // Ignore all elements shich already have the desired settings applied.
    this.selections.forEach((val, key) => {
      if (val) {
        if ((autostart && !this.appsMap.get(key).autostart) || (!autostart && this.appsMap.get(key).autostart)) {
          elementsToChange.push(key);
        }
      }
    });

    // Ask for confirmation.
    const confirmationDialog = GeneralUtils.createConfirmationDialog(
      this.dialog, autostart ? 'apps.enable-autostart-selected-confirmation' : 'apps.disable-autostart-selected-confirmation'
    );

    confirmationDialog.componentInstance.operationAccepted.subscribe(() => {
      confirmationDialog.componentInstance.showProcessing();

      this.changeAppsValRecursively(elementsToChange, true, autostart, confirmationDialog);
    });
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
        icon: app.status === 1 ? 'stop' : 'play_arrow',
        label: 'apps.' + (app.status === 1 ? 'stop-app' : 'start-app'),
      },
      {
        icon: app.autostart ? 'close' : 'done',
        label: app.autostart ? 'apps.apps-list.disable-autostart' : 'apps.apps-list.enable-autostart',
      }
    ];

    if (this.appsWithConfig.has(app.name)) {
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
        this.config(app);
      }
    });
  }

  /**
   * Starts or stops a specific app.
   */
  changeAppState(app: Application): void {
    if (app.status !== 1) {
      this.changeSingleAppVal(
        this.startChangingAppState(app.name, app.status !== 1)
      );
    } else {
      // Ask for confirmation if the app is going to be stopped.
      const confirmationDialog = GeneralUtils.createConfirmationDialog(this.dialog, 'apps.stop-confirmation');

      confirmationDialog.componentInstance.operationAccepted.subscribe(() => {
        confirmationDialog.componentInstance.showProcessing();

        this.changeSingleAppVal(
          this.startChangingAppState(app.name, app.status !== 1),
          confirmationDialog
        );
      });
    }
  }

  /**
   * Changes the autostart setting of a specific app.
   */
  changeAppAutostart(app: Application): void {
    const confirmationDialog = GeneralUtils.createConfirmationDialog(
      this.dialog, app.autostart ? 'apps.disable-autostart-confirmation' : 'apps.enable-autostart-confirmation'
    );

    confirmationDialog.componentInstance.operationAccepted.subscribe(() => {
      confirmationDialog.componentInstance.showProcessing();

      this.changeSingleAppVal(
        this.startChangingAppAutostart(app.name, !app.autostart),
        confirmationDialog
      );
    });
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
    if (app.status === 1) {
      LogComponent.openDialog(this.dialog, app);
    } else {
      this.snackbarService.showError('apps.apps-list.unavailable-logs-error');
    }
  }

  /**
   * Shows the appropriate modal window for configuring the app.
   */
  config(app: Application): void {
    if (app.name === 'skysocks' || app.name === 'vpn-server') {
      SkysocksSettingsComponent.openDialog(this.dialog, app);
    } else if (app.name === 'skysocks-client' || app.name === 'vpn-client') {
      SkysocksClientSettingsComponent.openDialog(this.dialog, app);
    } else {
      this.snackbarService.showError('apps.error');
    }
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
    return this.appsService.changeAppState(NodeComponent.getCurrentNodeKey(), appName, startApp);
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
