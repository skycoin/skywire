import { Component, Inject, OnInit } from '@angular/core';
import { MAT_DIALOG_DATA, MatDialog, MatDialogRef, MatDialogConfig } from '@angular/material/dialog';
import { UntypedFormGroup, UntypedFormBuilder } from '@angular/forms';

import { AppConfig } from 'src/app/app.config';
import { FilterFieldTypes, CompleteFilterProperties } from 'src/app/utils/filters';

/**
 * Params for FiltersSelectionComponent.
 */
export interface FiltersSelectiondParams {
  /**
   * Properties of the filters.
   */
  filterPropertiesList: CompleteFilterProperties[];
  /**
   * Object with the current value of the filters.
   */
  currentFilters: any;
}

/**
 * Generic modal window for selecting the filters for a list shown in the app. If the user
 * accepts the changes, the modal window is closed and an object with the selected filters is
 * returned in the "afterClosed" envent. The returned object will contain properties with the
 * names set in the keyNameInFiltersObject properties of the filterPropertiesList list provided
 * when opening the window, and the value of those properties will be the values selected by
 * the user.
 */
@Component({
  selector: 'app-filters-selection',
  templateUrl: './filters-selection.component.html',
  styleUrls: ['./filters-selection.component.scss']
})
export class FiltersSelectionComponent implements OnInit {
  form: UntypedFormGroup;
  filterFieldTypes = FilterFieldTypes;

  /**
   * Opens the modal window. Please use this function instead of opening the window "by hand".
   * @param filterFielsdParams List of the fields the window will show.
   */
  public static openDialog(dialog: MatDialog, filterFielsdParams: FiltersSelectiondParams): MatDialogRef<FiltersSelectionComponent, any> {
    const config = new MatDialogConfig();
    config.data = filterFielsdParams;
    config.autoFocus = false;
    config.width = AppConfig.smallModalWidth;

    return dialog.open(FiltersSelectionComponent, config);
  }

  constructor(
    @Inject(MAT_DIALOG_DATA) public data: FiltersSelectiondParams,
    public dialogRef: MatDialogRef<FiltersSelectionComponent>,
    private formBuilder: UntypedFormBuilder,
  ) { }

  ngOnInit() {
    // Create the form.
    const formFields = {};
    this.data.filterPropertiesList.forEach(properties => {
      formFields[properties.keyNameInFiltersObject] = [this.data.currentFilters[properties.keyNameInFiltersObject]];
    });

    this.form = this.formBuilder.group(formFields);
  }

  // Closes the modal window and returns the selected filters.
  apply() {
    const response = {};

    // Build the response object.
    this.data.filterPropertiesList.forEach(properties => {
      response[properties.keyNameInFiltersObject] =
        (this.form.get(properties.keyNameInFiltersObject).value as string).trim();
    });

    this.dialogRef.close(response);
  }
}
