import { Component, Input, OnDestroy } from '@angular/core';
import { MatDialog } from '@angular/material/dialog';
import { Subscription } from 'rxjs';
import { ActivatedRoute, Router } from '@angular/router';
import { TranslateService } from '@ngx-translate/core';

import { AppConfig } from '../../../../../app.config';
import GeneralUtils from '../../../../../utils/generalUtils';
import { SnackbarService } from '../../../../../services/snackbar.service';
import { SelectableOption, SelectOptionComponent } from 'src/app/components/layout/select-option/select-option.component';
import { FilterProperties, FilterFieldTypes } from 'src/app/utils/filters';
import { LabeledElementTypes, StorageService, LabelInfo } from 'src/app/services/storage.service';
import { DataSorter, SortingColumn, SortingModes } from 'src/app/utils/lists/data-sorter';
import { DataFilterer } from 'src/app/utils/lists/data-filterer';

/**
 * Shows the list of saved labels. It can be used to show a short preview, with just some
 * elements and a link for showing the rest; or the full list, with pagination controls.
 */
@Component({
  selector: 'app-label-list',
  templateUrl: './label-list.component.html',
  styleUrls: ['./label-list.component.scss']
})
export class LabelListComponent implements OnDestroy {
  // Small text for identifying the list, needed for the helper objects.
  private readonly listId = 'll';

  // Vars with the data of the columns used for sorting the data.
  labelSortData = new SortingColumn(['label'], 'labels.label', SortingModes.Text);
  idSortData = new SortingColumn(['id'], 'labels.id', SortingModes.Text);
  typeSortData = new SortingColumn(['identifiedElementType_sort'], 'labels.type', SortingModes.Text);

  private dataSortedSubscription: Subscription;
  private dataFiltererSubscription: Subscription;
  // Objects in charge of sorting and filtering the data.
  dataSorter: DataSorter;
  dataFilterer: DataFilterer;

  dataSource: LabelInfo[];
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
    this.dataSorter.setData(this.filteredLabels);
  }

  allLabels: LabelInfo[];
  filteredLabels: LabelInfo[];
  labelsToShow: LabelInfo[];
  numberOfPages = 1;
  currentPage = 1;
  // Used as a helper var, as the URL is reade asynchronously.
  currentPageInUrl = 1;

  // Array with the properties of the columns that can be used for filtering the data.
  filterProperties: FilterProperties[] = [
    {
      filterName: 'labels.filter-dialog.label',
      keyNameInElementsArray: 'label',
      type: FilterFieldTypes.TextInput,
      maxlength: 100,
    },
    {
      filterName: 'labels.filter-dialog.id',
      keyNameInElementsArray: 'id',
      type: FilterFieldTypes.TextInput,
      maxlength: 66,
    },
    {
      filterName: 'labels.filter-dialog.type',
      keyNameInElementsArray: 'identifiedElementType',
      type: FilterFieldTypes.Select,
      printableLabelsForValues: [
        {
          value: '',
          label: 'labels.filter-dialog.type-options.any',
        },
        {
          value: LabeledElementTypes.Node,
          label: 'labels.filter-dialog.type-options.visor',
        },
        {
          value: LabeledElementTypes.DmsgServer,
          label: 'labels.filter-dialog.type-options.dmsg-server',
        },
        {
          value: LabeledElementTypes.Transport,
          label: 'labels.filter-dialog.type-options.transport',
        }
      ],
    }
  ];

  private navigationsSubscription: Subscription;

  constructor(
    private dialog: MatDialog,
    private route: ActivatedRoute,
    private router: Router,
    private snackbarService: SnackbarService,
    private translateService: TranslateService,
    private storageService: StorageService,
  ) {
    // Initialize the data sorter.
    const sortableColumns: SortingColumn[] = [
      this.labelSortData,
      this.idSortData,
      this.typeSortData,
    ];
    this.dataSorter = new DataSorter(this.dialog, this.translateService, this.storageService, sortableColumns, 0, this.listId);
    this.dataSortedSubscription = this.dataSorter.dataSorted.subscribe(() => {
      // When this happens, the data in allLabels has already been sorted.
      this.recalculateElementsToShow();
    });

    this.dataFilterer = new DataFilterer(this.dialog, this.route, this.router, this.filterProperties, this.listId);
    this.dataFiltererSubscription = this.dataFilterer.dataFiltered.subscribe(data => {
      this.filteredLabels = data;
      this.dataSorter.setData(this.filteredLabels);
    });

    this.loadData();

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

    this.dataSortedSubscription.unsubscribe();
    this.dataSorter.dispose();

    this.dataFiltererSubscription.unsubscribe();
    this.dataFilterer.dispose();
  }

  /**
   * Loads the label list and saves it in this.allLabels.
   */
  private loadData() {
    this.allLabels = this.storageService.getSavedLabels();

    // Add the type dor data to the array.
    this.allLabels.forEach(label => {
      label['identifiedElementType_sort'] = this.getLabelTypeIdentification(label)[0];
    });

    this.dataFilterer.setData(this.allLabels);
  }

  /**
   * Allows to get the elements needed for identifiying the label type. It returns an array
   * with the type number as the first element and the translatable var with the name in the
   * second element. The number is just for being able to sort the list by type without having
   * problems with the language changes.
   */
  getLabelTypeIdentification(label: LabelInfo) {
    if (label.identifiedElementType === LabeledElementTypes.Node) {
      return ['1', 'labels.filter-dialog.type-options.visor'];
    } else if (label.identifiedElementType === LabeledElementTypes.DmsgServer) {
      return ['2', 'labels.filter-dialog.type-options.dmsg-server'];
    } else if (label.identifiedElementType === LabeledElementTypes.Transport) {
      return ['3', 'labels.filter-dialog.type-options.transport'];
    }
  }

  /**
   * Changes the selection state of an entry (modifies the state of its checkbox).
   */
  changeSelection(label: LabelInfo) {
    if (this.selections.get(label.id)) {
      this.selections.set(label.id, false);
    } else {
      this.selections.set(label.id, true);
    }
  }

  /**
   * Checks if at lest one entry has been selected via its checkbox.
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
   * Deletes the selected elements.
   */
  deleteSelected() {
    // Ask for confirmation.
    const confirmationDialog = GeneralUtils.createConfirmationDialog(this.dialog, 'labels.delete-selected-confirmation');

    confirmationDialog.componentInstance.operationAccepted.subscribe(() => {
      confirmationDialog.close();

      // Remove the elements.
      this.selections.forEach((val, key) => {
        if (val) {
          this.storageService.saveLabel(key, '', null);
        }
      });

      this.snackbarService.showDone('labels.deleted');
      this.loadData();
    });
  }

  /**
   * Opens the modal window used on small screens with the options of an element.
   */
  showOptionsDialog(label: LabelInfo) {
    const options: SelectableOption[] = [
      {
        icon: 'close',
        label: 'labels.delete',
      }
    ];

    SelectOptionComponent.openDialog(this.dialog, options, 'common.options').afterClosed().subscribe((selectedOption: number) => {
      if (selectedOption === 1) {
        this.delete(label.id);
      }
    });
  }

  /**
   * Deletes a specific element.
   */
  delete(id: string) {
    const confirmationDialog = GeneralUtils.createConfirmationDialog(this.dialog, 'labels.delete-confirmation');

    confirmationDialog.componentInstance.operationAccepted.subscribe(() => {
      confirmationDialog.close();
      this.storageService.saveLabel(id, '', null);
      this.snackbarService.showDone('labels.deleted');

      this.loadData();
    });
  }

  /**
   * Recalculates which elements should be shown on the UI.
   */
  private recalculateElementsToShow() {
    // Needed to prevent racing conditions.
    this.currentPage = this.currentPageInUrl;

    // Needed to prevent racing conditions.
    if (this.filteredLabels) {
      // Calculate the pagination values.
      const maxElements = this.showShortList_ ? AppConfig.maxShortListElements : AppConfig.maxFullListElements;
      this.numberOfPages = Math.ceil(this.filteredLabels.length / maxElements);
      if (this.currentPage > this.numberOfPages) {
        this.currentPage = this.numberOfPages;
      }

      // Limit the elements to show.
      const start = maxElements * (this.currentPage - 1);
      const end = start + maxElements;
      this.labelsToShow = this.filteredLabels.slice(start, end);

      // Create a map with the elements to show, as a helper.
      const currentElementsMap = new Map<string, boolean>();
      this.labelsToShow.forEach(label => {
        currentElementsMap.set(label.id, true);

        // Add to the selections map the elements that are going to be shown.
        if (!this.selections.has(label.id)) {
          this.selections.set(label.id, false);
        }
      });

      // Remove from the selections map the elements that are not going to be shown.
      const keysToRemove: string[] = [];
      this.selections.forEach((value, key) => {
        if (!currentElementsMap.has(key)) {
          keysToRemove.push(key);
        }
      });
      keysToRemove.forEach(key => {
        this.selections.delete(key);
      });

    } else {
      this.labelsToShow = null;
      this.selections = new Map<string, boolean>();
    }

    this.dataSource = this.labelsToShow;
  }
}
