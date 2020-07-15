import { Component, Inject, OnInit } from '@angular/core';
import { MAT_DIALOG_DATA, MatDialog, MatDialogRef, MatDialogConfig } from '@angular/material/dialog';
import { FormGroup, FormBuilder } from '@angular/forms';

import { AppConfig } from 'src/app/app.config';
import { FilterKeysAssociation } from 'src/app/utils/filters';

/**
 * Field types FiltersSelectionComponent can show.
 */
export enum FilterFieldTypes {
  /**
   * Field in which the user can enter text freely. When using this type, the maxlength property
   * of the parent FilterFieldParams object must have a value.
   */
  TextInput = 'TextInput',
  /**
   * Field in which the user must select the value from a list. When using this type, the option
   * list will be created using the filterKeysAssociation.printableLabelsForValues list of the
   * parent FilterFieldParams object.
   */
  Select = 'Select',
}

/**
 * Params for the fields shown by FiltersSelectionComponent.
 */
export interface FilterFieldParams {
  /**
   * Type of the field to be shown in the form.
   */
  type: FilterFieldTypes;
  /**
   * Current value of the filter. Will be added to the form during creation.
   */
  currentValue: string;
  /**
   * Object with the data needed for associating the filters with the fields and response data.
   */
  filterKeysAssociation: FilterKeysAssociation;
  /**
   * Max allowed length for the filter, if the field is text input.
   */
  maxlength?: number;
}

/**
 * Generic modal window for selecting the filters for a list shown in the app. If the user
 * accepts the changes, the modal window is closed and an object with the selected filters is
 * returned in the "afterClosed" envent. The returned object will contain properties with the
 * names set in the keyNameInFiltersObject properties of the filterFielsdParams list provided
 * when opening the window, and the value of those properties will be the values selected by
 * the user.
 */
@Component({
  selector: 'app-filters-selection',
  templateUrl: './filters-selection.component.html',
  styleUrls: ['./filters-selection.component.scss']
})
export class FiltersSelectionComponent implements OnInit {
  form: FormGroup;
  filterFieldTypes = FilterFieldTypes;

  /**
   * Opens the modal window. Please use this function instead of opening the window "by hand".
   * @param filterFielsdParams List of the fields the window will show.
   */
  public static openDialog(dialog: MatDialog, filterFielsdParams: FilterFieldParams[]): MatDialogRef<FiltersSelectionComponent, any> {
    const config = new MatDialogConfig();
    config.data = filterFielsdParams;
    config.autoFocus = false;
    config.width = AppConfig.smallModalWidth;

    return dialog.open(FiltersSelectionComponent, config);
  }

  constructor(
    @Inject(MAT_DIALOG_DATA) private data: FilterFieldParams[],
    private dialogRef: MatDialogRef<FiltersSelectionComponent>,
    private formBuilder: FormBuilder,
  ) { }

  ngOnInit() {
    // Create the form.
    const formFields = {};
    this.data.forEach(fieldParams => {
      formFields[fieldParams.filterKeysAssociation.keyNameInFiltersObject] = [fieldParams.currentValue];
    });

    this.form = this.formBuilder.group(formFields);
  }

  // Closes the modal window and returns the selected filters.
  apply() {
    const response = {};

    // Build the response object.
    this.data.forEach(fieldParams => {
      response[fieldParams.filterKeysAssociation.keyNameInFiltersObject] =
        (this.form.get(fieldParams.filterKeysAssociation.keyNameInFiltersObject).value as string).trim();
    });

    this.dialogRef.close(response);
  }
}
