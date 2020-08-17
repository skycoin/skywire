import { Component, OnInit, ViewChild, OnDestroy, ElementRef, Inject } from '@angular/core';
import { FormBuilder, FormGroup, Validators } from '@angular/forms';
import { MatDialogRef, MatDialog, MatDialogConfig, MAT_DIALOG_DATA } from '@angular/material/dialog';
import { Subscription } from 'rxjs';

import { ButtonComponent } from '../../../../../layout/button/button.component';
import { NodeComponent } from '../../../node.component';
import { SnackbarService } from '../../../../../../services/snackbar.service';
import { AppConfig } from 'src/app/app.config';
import { processServiceError } from 'src/app/utils/errors';
import { OperationError } from 'src/app/utils/operation-error';
import { AppsService } from 'src/app/services/apps.service';
import GeneralUtils from 'src/app/utils/generalUtils';
import { Application, ProxyDiscoveryEntry } from 'src/app/app.datatypes';
import { ProxyDiscoveryService } from 'src/app/services/proxy-discovery.service';
import { EditSkysocksClientNoteComponent } from './edit-skysocks-client-note/edit-skysocks-client-note.component';
import { SelectableOption, SelectOptionComponent } from 'src/app/components/layout/select-option/select-option.component';
import {
  SkysocksClientFilterComponent,
  SkysocksClientFilters,
  FilterWindowData
} from './skysocks-client-filter/skysocks-client-filter.component';
import { countriesList } from 'src/app/utils/countries-list';

/**
 * Data of the entries from the history.
 */
export interface HistoryEntry {
  /**
   * Remote public key.
   */
  key: string;
  /**
   * If true, the user entered the data manually using the form. If false, the data was obtained
   * from the discovery service.
   */
  enteredManually: boolean;
  /**
   * Location of the visor. Only if it was obtained from the discovery service.
   */
  location?: string;
  /**
   * Custom note added by the user.
   */
  note?: string;
}

/**
 * Modal window used for configuring the Vpn-Client and Skysocks-Client apps.
 */
@Component({
  selector: 'app-skysocks-client-settings',
  templateUrl: './skysocks-client-settings.component.html',
  styleUrls: ['./skysocks-client-settings.component.scss']
})
export class SkysocksClientSettingsComponent implements OnInit, OnDestroy {
  // Keys for saving the history in persistent storage.
  private readonly socksHistoryStorageKey = 'SkysocksClientHistory_';
  private readonly vpnHistoryStorageKey = 'VpnClientHistory_';
  // Max elements the history can contain.
  readonly maxHistoryElements = 10;
  // How many elements to show per page on the proxy discovery tab.
  readonly maxElementsPerPage = 10;

  @ViewChild('button') button: ButtonComponent;
  @ViewChild('firstInput') firstInput: ElementRef;
  form: FormGroup;
  // Entries to show on the history.
  history: HistoryEntry[];

  // Proxies or vpn servers obtained from the discovery service.
  proxiesFromDiscovery: ProxyDiscoveryEntry[];
  // List with the countries returned by the discovery service.
  countriesFromDiscovery: Set<string> = new Set();
  // Filtered proxies or vpn servers.
  filteredProxiesFromDiscovery: ProxyDiscoveryEntry[];
  // Proxies or vpn servers to show in the currently selected page.
  proxiesFromDiscoveryToShow: ProxyDiscoveryEntry[];
  // If the system is still getting the proxies or vpn servers from the discovery service.
  loadingFromDiscovery = true;
  // How many pages with proxies or vpn servers there are.
  numberOfPages = 1;
  // Current page.
  currentPage = 1;
  // Which elements are being shown in the currently selected page.
  currentRange = '1 - 1';

  // Current filters for the list.
  currentFilters = new SkysocksClientFilters();
  // Texts to be shown on the filter button. Each element represents a filter and has 3
  // elements. The fist one is a translatable var which describes the filter, the second one has
  // the value selected by the user if it is a variable for the translate pipe and the third one
  // has the value selected by the user if the translate pipe is not needed,
  currentFiltersTexts: string[][] = [];

  // True if configuring Vpn-Client, false if configuring Skysocks-Client.
  configuringVpn = false;

  // If the operation in currently being made.
  private working = false;
  private operationSubscription: Subscription;
  private discoverySubscription: Subscription;

  /**
   * Opens the modal window. Please use this function instead of opening the window "by hand".
   */
  public static openDialog(dialog: MatDialog, app: Application): MatDialogRef<SkysocksClientSettingsComponent, any> {
    const config = new MatDialogConfig();
    config.data = app;
    config.autoFocus = false;
    config.width = AppConfig.largeModalWidth;

    return dialog.open(SkysocksClientSettingsComponent, config);
  }

  constructor(
    @Inject(MAT_DIALOG_DATA) private data: Application,
    private dialogRef: MatDialogRef<SkysocksClientSettingsComponent>,
    private appsService: AppsService,
    private formBuilder: FormBuilder,
    private snackbarService: SnackbarService,
    private dialog: MatDialog,
    private proxyDiscoveryService: ProxyDiscoveryService,
  ) {
    if (data.name.toLocaleLowerCase().indexOf('vpn') !== -1) {
      this.configuringVpn = true;
    }
  }

  ngOnInit() {
    // Get the proxies or vpn servers from the discovery service.
    this.discoverySubscription = this.proxyDiscoveryService.getServices(!this.configuringVpn).subscribe(response => {
      this.proxiesFromDiscovery = response;

      // Save all countries.
      this.proxiesFromDiscovery.forEach(entry => {
        if (entry.country) {
          this.countriesFromDiscovery.add(entry.country.toUpperCase());
        }
      });

      this.filterProxies();
      this.loadingFromDiscovery = false;
    });

    // Get the history.
    const retrievedHistory = localStorage.getItem(this.configuringVpn ? this.vpnHistoryStorageKey : this.socksHistoryStorageKey);
    if (retrievedHistory) {
      this.history = JSON.parse(retrievedHistory);
    } else {
      this.history = [];
    }

    // Get the current value saved on the visor, if it was returned by the API.
    let currentVal = '';
    if (this.data.args && this.data.args.length > 0) {
      for (let i = 0; i < this.data.args.length; i++) {
        if (this.data.args[i] === '-srv' && i + 1 < this.data.args.length) {
          currentVal = this.data.args[i + 1];
        }
      }
    }

    this.form = this.formBuilder.group({
      'pk': [currentVal, Validators.compose([
        Validators.required,
        Validators.minLength(66),
        Validators.maxLength(66),
        Validators.pattern('^[0-9a-fA-F]+$')])
      ],
    });

    setTimeout(() => (this.firstInput.nativeElement as HTMLElement).focus());
  }

  ngOnDestroy() {
    this.discoverySubscription.unsubscribe();
    if (this.operationSubscription) {
      this.operationSubscription.unsubscribe();
    }
  }

  // Opens the modal window for selecting the filters.
  changeFilters() {
    const countries: string[] = [];
    this.countriesFromDiscovery.forEach(v => countries.push(v));

    const data: FilterWindowData = {
      currentFilters: this.currentFilters,
      availableCountries: countries,
    };

    SkysocksClientFilterComponent.openDialog(this.dialog, data).afterClosed().subscribe(response => {
      if (response) {
        this.currentFilters = response;
        this.filterProxies();
      }
    });
  }

  /**
   * Returns an array that can be used to highlight a filter term in a string. No html tags
   * are returned to avoid security problems.
   * @param completeText Text string where the filter term must be highlighted.
   * @param filter term used for filtering the list.
   * @returns An array in which the value of completeText has been divided. Each even element
   * has a part of the text which must NOT be highlighted and each odd element has a part
   * which must be highlighted.
   */
  getHighlightedTextParts(completeText: string, filter: string): string[] {
    if (!filter) {
      return [completeText];
    }

    // Lowercase version for comparations.
    const lowercaseCompleteText = completeText.toLowerCase();
    const lowercaseFilter = filter.toLowerCase();

    let process = true;
    let currentIndex = 0;

    const response: string[] = [];

    while (process) {
      // Get the next part where the filter term is.
      const index = lowercaseCompleteText.indexOf(lowercaseFilter, currentIndex);

      if (index === -1) {
        process = false;
      } else {
        // Include the part which is before the term.
        response.push(completeText.substring(currentIndex, index));
        // Include the term as it is in the original string.
        response.push(completeText.substring(index, index + filter.length));

        currentIndex = index + filter.length;
      }
    }

    // Add the rest of the text.
    response.push(completeText.substring(currentIndex));

    return response;
  }

  // Filters the elements obtained from the discovery service using the filters selected by
  // the user.
  private filterProxies() {
    if (!this.currentFilters.country && !this.currentFilters.location && !this.currentFilters.key) {
      this.filteredProxiesFromDiscovery = this.proxiesFromDiscovery;
    } else {
      this.filteredProxiesFromDiscovery = this.proxiesFromDiscovery.filter(proxy => {
        if (
          this.currentFilters.country &&
          (!proxy.country || !proxy.country.toUpperCase().includes(this.currentFilters.country.toUpperCase()))
        ) {
          return false;
        }
        if (this.currentFilters.location && !proxy.location.toLowerCase().includes(this.currentFilters.location.toLowerCase())) {
          return false;
        }
        if (this.currentFilters.key && !proxy.address.toLowerCase().includes(this.currentFilters.key.toLowerCase())) {
          return false;
        }

        return true;
      });
    }

    this.updateCurrentFilters();
    this.updatePagination();
  }

  // Updates the texts of the filter button.
  private updateCurrentFilters() {
    this.currentFiltersTexts = [];

    if (this.currentFilters.country) {
      const country =
        countriesList[this.currentFilters.country.toUpperCase()] ?
        countriesList[this.currentFilters.country.toUpperCase()] :
        this.currentFilters.country.toUpperCase();

      this.currentFiltersTexts.push(['apps.vpn-socks-client-settings.filter-dialog.country', '', country]);
    }
    if (this.currentFilters.location) {
      this.currentFiltersTexts.push(['apps.vpn-socks-client-settings.filter-dialog.location', '', this.currentFilters.location]);
    }
    if (this.currentFilters.key) {
      this.currentFiltersTexts.push(['apps.vpn-socks-client-settings.filter-dialog.pub-key', '', this.currentFilters.key]);
    }
  }

  // Updates the vars related to the pagination of the proxy discovery tab and shows
  // the first page.
  private updatePagination() {
    this.currentPage = 1;
    this.numberOfPages = Math.ceil(this.filteredProxiesFromDiscovery.length / this.maxElementsPerPage);
    this.showCurrentPage();
  }

  // Goes to the next page in the proxy discovery tab.
  goToNextPage() {
    if (this.currentPage >= this.numberOfPages) {
      return;
    }

    this.currentPage += 1;
    this.showCurrentPage();
  }

  // Goes to the previous page in the proxy discovery tab.
  goToPreviousPage() {
    if (this.currentPage <= 1) {
      return;
    }

    this.currentPage -= 1;
    this.showCurrentPage();
  }

  // Updates the UI to show the elements of the page indicated in the currentPage var.
  private showCurrentPage() {
    // Update the elements to show.
    this.proxiesFromDiscoveryToShow = this.filteredProxiesFromDiscovery.slice(
      (this.currentPage - 1) * this.maxElementsPerPage,
      this.currentPage * this.maxElementsPerPage
    );

    // Update the text with the range currently shown.
    this.currentRange = (((this.currentPage - 1) * this.maxElementsPerPage) + 1) + ' - ';
    if (this.currentPage < this.numberOfPages) {
      this.currentRange += (this.currentPage * this.maxElementsPerPage) + '';
    } else {
      this.currentRange += this.filteredProxiesFromDiscovery.length + '';
    }
  }

  // Opens the modal window used on small screens with the options of an history entry.
  openHistoryOptions(historyEntry: HistoryEntry) {
    const options: SelectableOption[] = [
      {
        icon: 'chevron_right',
        label: 'apps.vpn-socks-client-settings.use',
      },
      {
        icon: 'edit',
        label: 'apps.vpn-socks-client-settings.change-note',
      },
      {
        icon: 'close',
        label: 'apps.vpn-socks-client-settings.remove-entry',
      }
    ];

    SelectOptionComponent.openDialog(this.dialog, options, 'common.options').afterClosed().subscribe((selectedOption: number) => {
      if (selectedOption === 1) {
        this.saveChanges(historyEntry.key, historyEntry.enteredManually, historyEntry.location, historyEntry.note);
      } else if (selectedOption === 2) {
        this.changeNote(historyEntry);
      } else if (selectedOption === 3) {
        this.removeFromHistory(historyEntry.key);
      }
    });
  }

  // Removes an element from the history.
  removeFromHistory(key: String) {
    // Ask for confirmation.
    const confirmationMsg = 'apps.vpn-socks-client-settings.remove-from-history-confirmation';
    const confirmationDialog = GeneralUtils.createConfirmationDialog(this.dialog, confirmationMsg);

    confirmationDialog.componentInstance.operationAccepted.subscribe(() => {
      this.history = this.history.filter(value => value.key !== key);
      const dataToSave = JSON.stringify(this.history);
      localStorage.setItem(this.configuringVpn ? this.vpnHistoryStorageKey : this.socksHistoryStorageKey, dataToSave);

      confirmationDialog.close();
    });
  }

  // Opens the modal window for changing the personal note of an history entry.
  changeNote(entry: HistoryEntry) {
    EditSkysocksClientNoteComponent.openDialog(this.dialog, entry.note).afterClosed().subscribe((response: string) => {
      if (response) {
        // Remove the "-" char the modal window adds at the start of the note.
        response = response.substr(1, response.length - 1);

        // Change the note.
        this.history.forEach(value => {
          if (value.key === entry.key) {
            value.note = response;
          }
        });

        // Save the changes..
        const dataToSave = JSON.stringify(this.history);
        localStorage.setItem(this.configuringVpn ? this.vpnHistoryStorageKey : this.socksHistoryStorageKey, dataToSave);

        if (!response) {
          this.snackbarService.showWarning('apps.vpn-socks-client-settings.default-note-warning');
        } else {
          this.snackbarService.showDone('apps.vpn-socks-client-settings.changes-made');
        }
      }
    });
  }

  /**
   * Saves the settings. If no argument is provided, the function will take the public key
   * from the form and fill the rest of the data. The arguments are mainly for elements selected
   * from the discovery list and entries from the history.
   * @param publicKey New public key to be used.
   * @param enteredManually If the user manually entered the data using the form.
   * @param location Location of the server.
   * @param note Personal note for the history.
   */
  saveChanges(publicKey: string = null, enteredManually: boolean = null, location: string = null, note: string = null) {
    // If no public key was provided, the data will be retrieved from the form, so the form
    // must be valid. Also, the operation can not continue if the component is already working.
    if ((!this.form.valid && !publicKey) || this.working) {
      return;
    }

    enteredManually = publicKey ? enteredManually : true;
    publicKey = publicKey ? publicKey : this.form.get('pk').value;

    // Ask for confirmation.
    const confirmationMsg = 'apps.vpn-socks-client-settings.change-key-confirmation';
    const confirmationDialog = GeneralUtils.createConfirmationDialog(this.dialog, confirmationMsg);
    confirmationDialog.componentInstance.operationAccepted.subscribe(() => {
      confirmationDialog.close();
      this.continueSavingChanges(publicKey, enteredManually, location, note);
    });
  }

  // Makes the call to the hypervisor API for changing the configuration.
  private continueSavingChanges(publicKey: string, enteredManually: boolean, location: string, note: string) {
    this.button.showLoading();
    this.working = true;

    this.operationSubscription = this.appsService.changeAppSettings(
      // The node pk is obtained from the currently openned node page.
      NodeComponent.getCurrentNodeKey(),
      this.data.name,
      { pk: publicKey },
    ).subscribe(
      () => this.onSuccess(publicKey, enteredManually, location, note),
      err => this.onError(err),
    );
  }

  private onSuccess(publicKey: string, enteredManually: boolean, location: string, note: string) {
    // Remove any repeated entry from the history.
    this.history = this.history.filter(value => value.key !== publicKey);

    // Add the available data to the history entry.
    const newEntry: HistoryEntry = {
      key: publicKey,
      enteredManually: enteredManually,
    };
    if (location) {
      newEntry.location = location;
    }
    if (note) {
      newEntry.note = note;
    }

    // Save the data on the history.
    this.history = [newEntry].concat(this.history);
    if (this.history.length > this.maxHistoryElements) {
      const itemsToRemove = this.history.length - this.maxHistoryElements;
      this.history.splice(this.history.length - itemsToRemove, itemsToRemove);
    }

    const dataToSave = JSON.stringify(this.history);
    localStorage.setItem(this.configuringVpn ? this.vpnHistoryStorageKey : this.socksHistoryStorageKey, dataToSave);

    // Close the window.
    NodeComponent.refreshCurrentDisplayedData();
    this.snackbarService.showDone('apps.vpn-socks-client-settings.changes-made');
    this.dialogRef.close();
  }

  private onError(err: OperationError) {
    this.working = false;
    this.button.showError();
    err = processServiceError(err);

    this.snackbarService.showError(err);
  }
}
