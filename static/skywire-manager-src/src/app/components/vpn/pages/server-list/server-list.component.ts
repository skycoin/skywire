import { Component, OnDestroy, OnInit } from '@angular/core';
import { Subscription } from 'rxjs';
import { MatDialog } from '@angular/material/dialog';
import { Router, ActivatedRoute } from '@angular/router';
import { TranslateService } from '@ngx-translate/core';

import { SortingModes, SortingColumn, DataSorter } from 'src/app/utils/lists/data-sorter';
import { DataFilterer } from 'src/app/utils/lists/data-filterer';
import { FilterProperties, FilterFieldTypes } from 'src/app/utils/filters';
import { TabButtonData } from '../../../layout/top-bar/top-bar.component';

enum Ratings {
  Gold = 0,
  Silver = 1,
  Bronze = 2,
}

class VpnServer {
  country: string;
  countryCode: string;
  name: string;
  location: string;
  pk: string;
  congestion: number;
  congestionRating: Ratings;
  latency: number;
  latencyRating: Ratings;
  hops: number;
  note: string;
}

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
  countrySortData = new SortingColumn(['country'], 'vpn.server-list.country-small-table-label', SortingModes.Text);
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
  // Objects in charge of sorting and filtering the data.
  dataSorter: DataSorter;
  dataFilterer: DataFilterer;

  loading = false;
  dataSource: VpnServer[];
  tabsData: TabButtonData[] = [];

  // Vars for the pagination functionality.
  allServers: VpnServer[];
  filteredServers: VpnServer[];
  serversToShow: VpnServer[];
  numberOfPages = 1;
  currentPage = 1;
  // Used as a helper var, as the URL is read asynchronously.
  currentPageInUrl = 1;

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

  constructor(
    private dialog: MatDialog,
    private router: Router,
    private translateService: TranslateService,
    route: ActivatedRoute,
  ) {
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

    this.dataFilterer = new DataFilterer(this.dialog, route, this.router, this.filterProperties, this.listId);
    this.dataFiltererSubscription = this.dataFilterer.dataFiltered.subscribe(data => {
      this.filteredServers = data;
      this.dataSorter.setData(this.filteredServers);
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

    // Data for populating the tab bar.
    this.tabsData = [
      {
        icon: 'view_headline',
        label: 'vpn.start',
        linkParts: ['/vpn'],
      },
      {
        icon: 'list',
        label: 'vpn.servers',
        linkParts: ['/vpn'],
      },
      {
        icon: 'flag',
        label: 'vpn.countries',
        linkParts: ['/vpn'],
      },
      {
        icon: 'settings',
        label: 'vpn.settings',
        linkParts: ['/vpn'],
      },
    ];
  }

  ngOnInit() {
    // Load the data.
    this.loadData();
  }

  ngOnDestroy() {
    this.dataSortedSubscription.unsubscribe();
    this.dataFiltererSubscription.unsubscribe();
    this.navigationsSubscription.unsubscribe();

    this.dataFilterer.dispose();
    this.dataSorter.dispose();
  }

  private loadData() {
    this.allServers = [{
      country: 'Australia',
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
      country: 'Brazil',
      countryCode: 'br',
      name: 'Test server 14',
      location: 'Rio de Janeiro - Brazil',
      pk: '024ec47420176680816e0406250e7156465e4531f5b26057c9f6297bb0303558c7',
      congestion: 20,
      congestionRating: Ratings.Silver,
      latency: 12345,
      latencyRating: Ratings.Gold,
      hops: 3,
      note: 'Note'
    }, {
      country: 'Germany',
      countryCode: 'de',
      name: 'Test server 20',
      location: 'Berlin - Germany',
      pk: '024ec47420176680816e0406250e7156465e4531f5b26057c9f6297bb0303558c7',
      congestion: 20,
      congestionRating: Ratings.Gold,
      latency: 123,
      latencyRating: Ratings.Bronze,
      hops: 7,
      note: 'Note'
    }];

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

  getLatencyValueString(latency: number): string {
    if (latency < 1000) {
      return 'time-in-ms';
    }

    return 'time-in-segs';
  }

  getPrintableLatency(latency: number): string {
    if (latency < 1000) {
      return latency + '';
    }

    return (latency / 1000).toFixed(1);
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
}
