import { Component, OnDestroy, OnInit } from '@angular/core';
import { Subscription } from 'rxjs';
import { MatDialog, MatDialogRef } from '@angular/material/dialog';
import { Router, ActivatedRoute } from '@angular/router';
import { TranslateService } from '@ngx-translate/core';

import { SortingModes, SortingColumn, DataSorter } from 'src/app/utils/lists/data-sorter';
import { DataFilterer } from 'src/app/utils/lists/data-filterer';
import { FilterProperties, FilterFieldTypes, PrintableLabel } from 'src/app/utils/filters';
import { countriesList } from 'src/app/utils/countries-list';
import { VpnClientDiscoveryService, VpnServer, Ratings } from 'src/app/services/vpn-client-discovery.service';
import { VpnHelpers } from '../../vpn-helpers';
import { VpnStatusComponent } from '../vpn-status/vpn-status.component';
import { VpnClientService, CheckPkResults } from 'src/app/services/vpn-client.service';
import GeneralUtils from 'src/app/utils/generalUtils';
import { ConfirmationComponent } from 'src/app/components/layout/confirmation/confirmation.component';
import { SnackbarService } from 'src/app/services/snackbar.service';

/**
 * Page for showing the vpn server list.
 */
@Component({
  selector: 'app-server-list',
  templateUrl: './server-list.component.html',
  styleUrls: ['./server-list.component.scss'],
})
export class ServerListComponent implements OnInit, OnDestroy {
  // Small text for identifying the list, needed for the helper objects.
  private readonly listId = 'vs';

  // How many elements can be shown per page.
  private readonly maxFullListElements = 50;

  // Vars with the data of the columns used for sorting the data.
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
  dataSource: VpnServer[];
  tabsData = VpnHelpers.vpnTabsData;

  // Vars for the pagination functionality.
  allServers: VpnServer[];
  filteredServers: VpnServer[];
  serversToShow: VpnServer[];
  numberOfPages = 1;
  currentPage = 1;
  // Used as a helper var, as the URL is read asynchronously.
  currentPageInUrl = 1;

  currentLocalPk: string;

  // Array with the properties of the columns that can be used for filtering the data.
  filterProperties: FilterProperties[] = [
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
    },
    {
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
    },
    {
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
    },
  ];

  private navigationsSubscription: Subscription;
  private dataSubscription: Subscription;

  constructor(
    private dialog: MatDialog,
    private router: Router,
    private translateService: TranslateService,
    private route: ActivatedRoute,
    private vpnClientDiscoveryService: VpnClientDiscoveryService,
    private vpnClientService: VpnClientService,
    private snackbarService: SnackbarService,
  ) {
    // Get the page requested in the URL.
    this.navigationsSubscription = route.paramMap.subscribe(params => {
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
    });
  }

  ngOnInit() {
    // Load the data.
    this.loadTestData();
  }

  ngOnDestroy() {
    this.dataSortedSubscription.unsubscribe();
    this.dataFiltererSubscription.unsubscribe();
    this.navigationsSubscription.unsubscribe();

    if (this.dataSubscription) {
      this.dataSubscription.unsubscribe();
    }

    this.closeCheckVpnSubscription();

    this.dataFilterer.dispose();
    this.dataSorter.dispose();
  }

  selectServer(server: VpnServer) {
    const result = this.vpnClientService.checkNewPk(server.pk);

    if (result === CheckPkResults.Busy) {
      this.snackbarService.showError('vpn.server-change.busy-error');

      return;
    }

    if (result === CheckPkResults.SamePkRunning) {
      this.snackbarService.showWarning('vpn.server-change.already-selected-warning');

      return;
    }

    if (result === CheckPkResults.MustStop) {
      const confirmationDialog =
        GeneralUtils.createConfirmationDialog(this.dialog, 'vpn.server-change.change-server-while-connected-confirmation');

        confirmationDialog.componentInstance.operationAccepted.subscribe(() => {
          confirmationDialog.componentInstance.closeModal();

          this.vpnClientService.changeServer(server.pk, null);
          this.router.navigate(['vpn', this.currentLocalPk, 'status']);
        });

        return;
    }

    if (result === CheckPkResults.SamePkStopped) {
      const confirmationDialog =
        GeneralUtils.createConfirmationDialog(this.dialog, 'vpn.server-change.start-same-server-confirmation');

        confirmationDialog.componentInstance.operationAccepted.subscribe(() => {
          confirmationDialog.componentInstance.closeModal();

          this.vpnClientService.start();
          this.router.navigate(['vpn', this.currentLocalPk, 'status']);
        });

        return;
    }

    this.vpnClientService.changeServer(server.pk, null);
    this.router.navigate(['vpn', this.currentLocalPk, 'status']);
  }

  private loadData() {
    // Get the vpn servers from the discovery service.
    this.dataSubscription = this.vpnClientDiscoveryService.getServers().subscribe(response => {
      this.allServers = response;

      this.loading = false;

      this.processAllServers();
    });
  }

  private loadTestData() {
    this.allServers = [{
      countryCode: 'au',
      name: 'Server name',
      location: 'Melbourne - Australia',
      pk: '024ec47420176680816e0406250e7156465e4531f5b26057c9f6297bb0303558c7',
      congestion: 20,
      congestionRating: Ratings.Gold,
      latency: 123,
      latencyRating: Ratings.Gold,
      hops: 3,
      note: 'Note'
    }, {
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
    }, {
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
    }];

    this.loading = false;

    this.processAllServers();
  }

  private processAllServers() {
    // Get the countries in the server list.
    const countriesSet = new Set<string>();
    this.allServers.forEach(server => {
      countriesSet.add(server.countryCode);
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
    const sortableColumns: SortingColumn[] = [
      this.countrySortData,
      this.nameSortData,
      this.locationSortData,
      this.pkSortData,
      this.congestionSortData,
      this.congestionRatingSortData,
      this.latencySortData,
      this.latencyRatingSortData,
      this.hopsSortData,
      this.noteSortData,
    ];
    this.dataSorter = new DataSorter(this.dialog, this.translateService, sortableColumns, 0, this.listId);
    this.dataSortedSubscription = this.dataSorter.dataSorted.subscribe(() => {
      // When this happens, the data in allServers has already been sorted.
      this.recalculateElementsToShow();
    });

    this.dataFilterer = new DataFilterer(this.dialog, this.route, this.router, this.filterProperties, this.listId);
    this.dataFiltererSubscription = this.dataFilterer.dataFiltered.subscribe(data => {
      this.filteredServers = data;
      this.dataSorter.setData(this.filteredServers);
    });

    this.dataFilterer.setData(this.allServers);
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
