import { Component, OnDestroy, OnInit } from '@angular/core';
import { Subscription, Observable } from 'rxjs';
import { MatDialog } from '@angular/material/dialog';
import { Router, ActivatedRoute } from '@angular/router';
import { TranslateService } from '@ngx-translate/core';

import { SortingModes, SortingColumn, DataSorter } from 'src/app/utils/lists/data-sorter';
import { DataFilterer } from 'src/app/utils/lists/data-filterer';
import { FilterProperties, FilterFieldTypes, PrintableLabel } from 'src/app/utils/filters';
import { countriesList } from 'src/app/utils/countries-list';
import { VpnClientDiscoveryService, VpnServer, Ratings } from 'src/app/services/vpn-client-discovery.service';
import { VpnHelpers } from '../../vpn-helpers';
import { VpnClientService } from 'src/app/services/vpn-client.service';
import { SnackbarService } from 'src/app/services/snackbar.service';
import { AddVpnServerComponent } from './add-vpn-server/add-vpn-server.component';
import { VpnSavedDataService, LocalServerData, ServerFlags } from 'src/app/services/vpn-saved-data.service';
import { SelectableOption, SelectOptionComponent } from 'src/app/components/layout/select-option/select-option.component';
import GeneralUtils from 'src/app/utils/generalUtils';

export enum Lists {
  Public = 'public',
  History = 'history',
  Favorites = 'favorites',
  Blocked = 'blocked',
}

interface VpnServerForList {
  countryCode: string;
  name: string;
  location: string;
  pk: string;
  congestion?: number;
  congestionRating?: Ratings;
  latency?: number;
  latencyRating?: Ratings;
  hops?: number;
  note: string;
  lastUsed?: number;
  inHistory?: boolean;
  flag?: ServerFlags;

  originalDiscoveryData?: VpnServer;
  originalLocalData?: LocalServerData;
}

/**
 * Page for showing the vpn server list.
 */
@Component({
  selector: 'app-server-list',
  templateUrl: './server-list.component.html',
  styleUrls: ['./server-list.component.scss'],
})
export class ServerListComponent implements OnDestroy {
  // Small text for identifying the list, needed for the helper objects.
  private listId: string;

  // How many elements can be shown per page.
  private readonly maxFullListElements = 50;

  // Vars with the data of the columns used for sorting the data.
  dateSortData = new SortingColumn(['lastUsed'], 'vpn.server-list.date-small-table-label', SortingModes.NumberReversed);
  countrySortData = new SortingColumn(['countryCode'], 'vpn.server-list.country-small-table-label', SortingModes.Text);
  nameSortData = new SortingColumn(['name'], 'vpn.server-list.name-small-table-label', SortingModes.Text);
  locationSortData = new SortingColumn(['location'], 'vpn.server-list.location-small-table-label', SortingModes.Text);
  pkSortData = new SortingColumn(['pk'], 'vpn.server-list.public-key-small-table-label', SortingModes.Text);
  congestionSortData = new SortingColumn(['congestion'], 'vpn.server-list.congestion-small-table-label', SortingModes.Number);
  congestionRatingSortData = new SortingColumn(
    ['congestionRating'],
    'vpn.server-list.congestion-rating-small-table-label',
    SortingModes.Number
  );
  latencySortData = new SortingColumn(['latency'], 'vpn.server-list.latency-small-table-label', SortingModes.Number);
  latencyRatingSortData = new SortingColumn(['latencyRating'], 'vpn.server-list.latency-rating-small-table-label', SortingModes.Number);
  hopsSortData = new SortingColumn(['hops'], 'vpn.server-list.hops-small-table-label', SortingModes.Number);
  noteSortData = new SortingColumn(['note'], 'vpn.server-list.note-small-table-label', SortingModes.Text);

  private dataSortedSubscription: Subscription;
  private dataFiltererSubscription: Subscription;
  private checkVpnSubscription: Subscription;
  // Objects in charge of sorting and filtering the data.
  dataSorter: DataSorter;
  dataFilterer: DataFilterer;

  loading = true;
  loadingBackendData = true;
  dataSource: VpnServerForList[];
  tabsData = VpnHelpers.vpnTabsData;

  // Vars for the pagination functionality.
  allServers: VpnServerForList[];
  filteredServers: VpnServerForList[];
  serversToShow: VpnServerForList[];
  numberOfPages = 1;
  currentPage = 1;
  // Used as a helper var, as the URL is read asynchronously.
  currentPageInUrl = 1;

  currentLocalPk: string;
  currentList = Lists.Public;
  lists = Lists;
  currentServer: LocalServerData;
  serverFlags = ServerFlags;

  // Array with the properties of the columns that can be used for filtering the data.
  filterProperties: FilterProperties[];

  private initialLoadStarted = false;

  private navigationsSubscription: Subscription;
  private dataSubscription: Subscription;
  private currentServerSubscription: Subscription;
  private backendDataSubscription: Subscription;

  constructor(
    private dialog: MatDialog,
    private router: Router,
    private translateService: TranslateService,
    private route: ActivatedRoute,
    private vpnClientDiscoveryService: VpnClientDiscoveryService,
    private vpnClientService: VpnClientService,
    private vpnSavedDataService: VpnSavedDataService,
    private snackbarService: SnackbarService,
  ) {
    // Get the page requested in the URL.
    this.navigationsSubscription = route.paramMap.subscribe(params => {
      if (params.has('type')) {
        if (params.get('type') === Lists.Favorites) {
          this.currentList = Lists.Favorites;
          this.listId = 'vfs';
        } else if (params.get('type') === Lists.Blocked) {
          this.currentList = Lists.Blocked;
          this.listId = 'vbs';
        } else if (params.get('type') === Lists.History) {
          this.currentList = Lists.History;
          this.listId = 'vhs';
        } else {
          this.currentList = Lists.Public;
          this.listId = 'vps';
        }
      } else {
        this.currentList = Lists.Public;
        this.listId = 'vps';
      }

      VpnHelpers.setDefaultTabForServerList(this.currentList);

      if (params.has('page')) {
        let selectedPage = Number.parseInt(params.get('page'), 10);
        if (isNaN(selectedPage) || selectedPage < 1) {
          selectedPage = 1;
        }

        if (params.has('key')) {
          this.currentLocalPk = params.get('key');
          VpnHelpers.changeCurrentPk(this.currentLocalPk);
          this.tabsData = VpnHelpers.vpnTabsData;
        }

        this.currentPageInUrl = selectedPage;

        this.recalculateElementsToShow();
      }

      if (!this.initialLoadStarted) {
        this.initialLoadStarted = true;

        if (this.currentList === Lists.Public) {
          this.loadTestData();
        } else {
          this.loadData();
        }
      }
    });

    this.currentServerSubscription = this.vpnSavedDataService.currentServerObservable.subscribe(server => this.currentServer = server);

    this.backendDataSubscription = this.vpnClientService.backendState.subscribe(data => {
      if (data) {
        this.loadingBackendData = false;
      }
    });
  }

  ngOnDestroy() {
    this.navigationsSubscription.unsubscribe();
    this.currentServerSubscription.unsubscribe();

    if (this.dataSortedSubscription) {
      this.dataSortedSubscription.unsubscribe();
    }
    if (this.dataFiltererSubscription) {
      this.dataFiltererSubscription.unsubscribe();
    }
    if (this.dataSubscription) {
      this.dataSubscription.unsubscribe();
    }

    this.closeCheckVpnSubscription();

    this.dataFilterer.dispose();
    this.dataSorter.dispose();
  }

  enterManually() {
    AddVpnServerComponent.openDialog(this.dialog, this.currentLocalPk);
  }

  selectServer(server: VpnServer) {
    VpnHelpers.processServerChange(
      this.router,
      this.vpnClientService,
      this.snackbarService,
      this.dialog,
      null,
      this.currentLocalPk,
      null,
      server,
      null
    );
  }

  openOptions(server: VpnServer) {
    const savedVersion = this.vpnSavedDataService.getSavedVersion(server.pk);

    const options: SelectableOption[] = [];
    const optionCodes: number[] = [];

    if (!savedVersion || savedVersion.flag !== ServerFlags.Favorite) {
      options.push({
        icon: 'star',
        label: 'vpn.server-list.options.make-favorite',
      });
      optionCodes.push(1);
    }

    if (savedVersion && savedVersion.flag === ServerFlags.Favorite) {
      options.push({
        icon: 'star_outline',
        label: 'vpn.server-list.options.remove-from-favorites',
      });
      optionCodes.push(-1);
    }

    if (!savedVersion || savedVersion.flag !== ServerFlags.Blocked) {
      options.push({
        icon: 'pan_tool',
        label: 'vpn.server-list.options.block',
      });
      optionCodes.push(2);
    }

    if (savedVersion && savedVersion.flag === ServerFlags.Blocked) {
      options.push({
        icon: 'thumb_up',
        label: 'vpn.server-list.options.unblock',
      });
      optionCodes.push(-2);
    }

    if (savedVersion && savedVersion.inHistory) {
      options.push({
        icon: 'delete',
        label: 'vpn.server-list.options.remove-from-history',
      });
      optionCodes.push(-3);
    }

    SelectOptionComponent.openDialog(this.dialog, options, 'common.options').afterClosed().subscribe((selectedOption: number) => {
      if (selectedOption) {
        let savedVersion_ = this.vpnSavedDataService.getSavedVersion(server.pk);
        if (!savedVersion_) {
          savedVersion_ = this.vpnSavedDataService.processFromDiscovery(server);
        }

        selectedOption -= 1;

        if (optionCodes[selectedOption] === 1) {
          if (savedVersion_.flag !== ServerFlags.Blocked) {
            this.vpnSavedDataService.changeFlag(savedVersion_, ServerFlags.Favorite);
            this.snackbarService.showDone('vpn.server-list.options.make-favorite-done');
            this.processAllServers();
          } else {
            const confirmationDialog = GeneralUtils.createConfirmationDialog(this.dialog, 'vpn.server-list.options.make-favorite-confirmation');
            confirmationDialog.componentInstance.operationAccepted.subscribe(() => {
              confirmationDialog.componentInstance.closeModal();
              this.vpnSavedDataService.changeFlag(savedVersion_, ServerFlags.Favorite);
              this.snackbarService.showDone('vpn.server-list.options.make-favorite-done');
              this.processAllServers();
            });
          }
        } else if (optionCodes[selectedOption] === -1) {
          this.vpnSavedDataService.changeFlag(savedVersion_, ServerFlags.None);
          this.snackbarService.showDone('vpn.server-list.options.remove-from-favorites-done');
          this.processAllServers();
        } else if (optionCodes[selectedOption] === 2) {
          if (savedVersion_.flag !== ServerFlags.Favorite) {
            this.vpnSavedDataService.changeFlag(savedVersion_, ServerFlags.Blocked);
            this.snackbarService.showDone('vpn.server-list.options.block-done');
            this.processAllServers();
          } else {
            const confirmationDialog = GeneralUtils.createConfirmationDialog(this.dialog, 'vpn.server-list.options.block-confirmation');
            confirmationDialog.componentInstance.operationAccepted.subscribe(() => {
              confirmationDialog.componentInstance.closeModal();
              this.vpnSavedDataService.changeFlag(savedVersion_, ServerFlags.Blocked);
              this.snackbarService.showDone('vpn.server-list.options.block-done');
              this.processAllServers();
            });
          }
        } else if (optionCodes[selectedOption] === -2) {
          this.vpnSavedDataService.changeFlag(savedVersion_, ServerFlags.None);
          this.snackbarService.showDone('vpn.server-list.options.unblock-done');
          this.processAllServers();
        } else if (optionCodes[selectedOption] === -3) {
          this.vpnSavedDataService.removeFromHistory(savedVersion_.pk);
          this.snackbarService.showDone('vpn.server-list.options.remove-from-history-done');
          this.processAllServers();
        }
      }
    });
  }

  private loadData() {
    if (this.currentList === Lists.Public) {
      // Get the vpn servers from the discovery service.
      this.dataSubscription = this.vpnClientDiscoveryService.getServers().subscribe(response => {
        this.allServers = response.map(server => {
          return {
            countryCode: server.countryCode,
            name: server.name,
            location: server.location,
            pk: server.pk,
            congestion: server.congestion,
            congestionRating: server.congestionRating,
            latency: server.latency,
            latencyRating: server.latencyRating,
            hops: server.hops,
            note: server.note,

            originalDiscoveryData: server,
          };
        });

        this.vpnSavedDataService.updateFromDiscovery(response);

        this.loading = false;

        this.processAllServers();
      });
    } else {
      let dataObservable: Observable<LocalServerData[]>;

      if (this.currentList === Lists.History) {
        dataObservable = this.vpnSavedDataService.history;
      } else if (this.currentList === Lists.Favorites) {
        dataObservable = this.vpnSavedDataService.favorites;
      } else {
        dataObservable = this.vpnSavedDataService.blocked;
      }

      this.dataSubscription = dataObservable.subscribe(response => {
        const processedList: VpnServerForList[] = [];
        response.forEach(server => {
          processedList.push({
            countryCode: server.countryCode,
            name: server.name,
            location: server.location,
            pk: server.pk,
            note: server.note,
            lastUsed: server.lastUsed,
            inHistory: server.inHistory,
            flag: server.flag,

            originalLocalData: server,
          });
        });

        this.allServers = processedList;

        this.loading = false;

        this.processAllServers();
      });
    }
  }

  private loadTestData() {
    setTimeout(() => {
      this.allServers = [];

      const server1 = {
        countryCode: 'au',
        name: 'Server name',
        location: 'Melbourne - Australia',
        pk: '024ec47420176680816e0406250e7156465e4531f5b26057c9f6297bb0303558c7',
        congestion: 20,
        congestionRating: Ratings.Gold,
        latency: 123,
        latencyRating: Ratings.Gold,
        hops: 3,
        note: 'Note',
      };
      this.allServers.push({...server1,
        originalDiscoveryData: server1
      });

      const server2 = {
        countryCode: 'br',
        name: 'Test server 14',
        location: 'Rio de Janeiro - Brazil',
        pk: '034ec47420176680816e0406250e7156465e4531f5b26057c9f6297bb0303558c7',
        congestion: 20,
        congestionRating: Ratings.Silver,
        latency: 12345,
        latencyRating: Ratings.Gold,
        hops: 3,
        note: 'Note'
      };
      this.allServers.push({...server2,
        originalDiscoveryData: server2
      });

      const server3 = {
        countryCode: 'de',
        name: 'Test server 20',
        location: 'Berlin - Germany',
        pk: '044ec47420176680816e0406250e7156465e4531f5b26057c9f6297bb0303558c7',
        congestion: 20,
        congestionRating: Ratings.Gold,
        latency: 123,
        latencyRating: Ratings.Bronze,
        hops: 7,
        note: 'Note'
      };
      this.allServers.push({...server3,
        originalDiscoveryData: server3,
      });

      this.vpnSavedDataService.updateFromDiscovery([server1, server2, server3]);

      this.loading = false;

      this.processAllServers();
    }, 100);
  }

  private processAllServers() {
    this.fillFilterPropertiesArray();

    const countriesSet = new Set<string>();
    this.allServers.forEach(server => {
      // Add the country to the countries list.
      countriesSet.add(server.countryCode);

      // Add the saved data, if any.
      const saveddata = this.vpnSavedDataService.getSavedVersion(server.pk);
      server.inHistory = saveddata ? saveddata.inHistory : false;
      server.flag = saveddata ? saveddata.flag : ServerFlags.None;
    });

    // Create a filter option for each country.
    let countriesFilteringLabels: PrintableLabel[] = [];
    countriesSet.forEach(v => {
      countriesFilteringLabels.push({
        label: this.getCountryName(v),
        value: v,
        image: '/assets/img/big-flags/' + v.toLowerCase() + '.png',
      });
    });

    // Sort the data and add an empty option at the top.
    countriesFilteringLabels.sort((a, b) => a.label.localeCompare(b.label));
    countriesFilteringLabels = [{
      label: 'vpn.server-list.filter-dialog.country-options.any',
      value: ''
    }].concat(countriesFilteringLabels);

    // Include the option for filtering by country.
    const countryFilter: FilterProperties = {
      filterName: 'vpn.server-list.filter-dialog.country',
      keyNameInElementsArray: 'countryCode',
      type: FilterFieldTypes.Select,
      printableLabelsForValues: countriesFilteringLabels,
      printableLabelGeneralSettings: {
        defaultImage: '/assets/img/big-flags/unknown.png',
        imageWidth: 20,
        imageHeight: 15,
      }
    };
    this.filterProperties = [countryFilter].concat(this.filterProperties);

    // Initialize the data sorter.
    const sortableColumns: SortingColumn[] = [];
    let defaultColumn: number;
    if (this.currentList === Lists.Public) {
      sortableColumns.push(this.countrySortData);
      sortableColumns.push(this.nameSortData);
      sortableColumns.push(this.locationSortData);
      sortableColumns.push(this.pkSortData);
      sortableColumns.push(this.congestionSortData);
      sortableColumns.push(this.congestionRatingSortData);
      sortableColumns.push(this.latencySortData);
      sortableColumns.push(this.latencyRatingSortData);
      sortableColumns.push(this.hopsSortData);
      sortableColumns.push(this.noteSortData);

      defaultColumn = 0;
    } else {
      if (this.currentList === Lists.History) {
        sortableColumns.push(this.dateSortData);
      }

      sortableColumns.push(this.countrySortData);
      sortableColumns.push(this.nameSortData);
      sortableColumns.push(this.locationSortData);
      sortableColumns.push(this.pkSortData);
      sortableColumns.push(this.noteSortData);

      defaultColumn = this.currentList === Lists.History ? 0 : 1;
    }
    this.dataSorter = new DataSorter(this.dialog, this.translateService, sortableColumns, defaultColumn, this.listId);
    this.dataSortedSubscription = this.dataSorter.dataSorted.subscribe(() => {
      // When this happens, the data in allServers has already been sorted.
      this.recalculateElementsToShow();
    });

    this.dataFilterer = new DataFilterer(this.dialog, this.route, this.router, this.filterProperties, this.listId);
    this.dataFiltererSubscription = this.dataFilterer.dataFiltered.subscribe(data => {
      this.filteredServers = data;
      this.dataSorter.setData(this.filteredServers);
    });

    let serversToUse: VpnServerForList[];
    if (this.currentList === Lists.Public) {
      serversToUse = this.allServers.filter(server => server.flag !== ServerFlags.Blocked);
    } else {
      serversToUse = this.allServers;
    }

    this.dataFilterer.setData(serversToUse);
  }

  private fillFilterPropertiesArray() {
    this.filterProperties = [
      {
        filterName: 'vpn.server-list.filter-dialog.name',
        keyNameInElementsArray: 'name',
        type: FilterFieldTypes.TextInput,
        maxlength: 100,
      },
      {
        filterName: 'vpn.server-list.filter-dialog.location',
        keyNameInElementsArray: 'location',
        type: FilterFieldTypes.TextInput,
        maxlength: 100,
      },
      {
        filterName: 'vpn.server-list.filter-dialog.public-key',
        keyNameInElementsArray: 'pk',
        type: FilterFieldTypes.TextInput,
        maxlength: 100,
      }
    ];

    if (this.currentList === Lists.Public) {
      this.filterProperties.push({
        filterName: 'vpn.server-list.filter-dialog.congestion-rating',
        keyNameInElementsArray: 'congestionRating',
        type: FilterFieldTypes.Select,
        printableLabelsForValues: [
          {
            value: '',
            label: 'vpn.server-list.filter-dialog.rating-options.any',
          },
          {
            value: Ratings.Gold + '',
            label: 'vpn.server-list.filter-dialog.rating-options.gold',
          },
          {
            value: Ratings.Silver + '',
            label: 'vpn.server-list.filter-dialog.rating-options.silver',
          },
          {
            value: Ratings.Bronze + '',
            label: 'vpn.server-list.filter-dialog.rating-options.bronze',
          }
        ],
      });

      this.filterProperties.push({
        filterName: 'vpn.server-list.filter-dialog.latency-rating',
        keyNameInElementsArray: 'latencyRating',
        type: FilterFieldTypes.Select,
        printableLabelsForValues: [
          {
            value: '',
            label: 'vpn.server-list.filter-dialog.rating-options.any',
          },
          {
            value: Ratings.Gold + '',
            label: 'vpn.server-list.filter-dialog.rating-options.gold',
          },
          {
            value: Ratings.Silver + '',
            label: 'vpn.server-list.filter-dialog.rating-options.silver',
          },
          {
            value: Ratings.Bronze + '',
            label: 'vpn.server-list.filter-dialog.rating-options.bronze',
          }
        ],
      });
    }
  }

  /**
   * Recalculates which elements should be shown on the UI.
   */
  private recalculateElementsToShow() {
    // Needed to prevent race conditions.
    this.currentPage = this.currentPageInUrl;

    // Needed to prevent race conditions.
    if (this.filteredServers) {
      // Calculate the pagination values.
      const maxElements = this.maxFullListElements;
      this.numberOfPages = Math.ceil(this.filteredServers.length / maxElements);
      if (this.currentPage > this.numberOfPages) {
        this.currentPage = this.numberOfPages;
      }

      // Limit the elements to show.
      const start = maxElements * (this.currentPage - 1);
      const end = start + maxElements;
      this.serversToShow = this.filteredServers.slice(start, end);
    } else {
      this.serversToShow = null;
    }

    this.dataSource = this.serversToShow;
  }

  getCountryName(countryCode: string): string {
    return countriesList[countryCode.toUpperCase()] ? countriesList[countryCode.toUpperCase()] : countryCode;
  }

  getLatencyValueString(latency: number): string {
    return VpnHelpers.getLatencyValueString(latency);
  }

  getPrintableLatency(latency: number): string {
    return VpnHelpers.getPrintableLatency(latency);
  }

  getCongestionTextColorClass(congestion: number): string {
    if (congestion < 60) {
      return 'green-value';
    } else if (congestion < 90) {
      return 'yellow-value';
    }

    return 'red-value';
  }

  getLatencyTextColorClass(latency: number): string {
    if (latency < 200) {
      return 'green-value';
    } else if (latency < 350) {
      return 'yellow-value';
    }

    return 'red-value';
  }

  getHopsTextColorClass(hops: number): string {
    if (hops < 5) {
      return 'green-value';
    } else if (hops < 9) {
      return 'yellow-value';
    }

    return 'red-value';
  }

  getRatingIcon(rating: Ratings): string {
    if (rating === Ratings.Gold) {
      return 'gold-rating';
    } else if (rating === Ratings.Silver) {
      return 'silver-rating';
    }

    return 'bronze-rating';
  }

  getRatingText(rating: Ratings): string {
    if (rating === Ratings.Gold) {
      return 'vpn.server-list.gold-rating-info';
    } else if (rating === Ratings.Silver) {
      return 'vpn.server-list.silver-rating-info';
    }

    return 'vpn.server-list.bronze-rating-info';
  }

  private closeCheckVpnSubscription() {
    if (this.checkVpnSubscription) {
      this.checkVpnSubscription.unsubscribe();
    }
  }
}
