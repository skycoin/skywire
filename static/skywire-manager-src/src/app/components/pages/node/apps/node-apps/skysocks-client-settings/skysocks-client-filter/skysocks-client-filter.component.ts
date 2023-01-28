import { Component, OnInit, Inject } from '@angular/core';
import { UntypedFormBuilder, UntypedFormGroup } from '@angular/forms';
import { MAT_DIALOG_DATA, MatDialogRef, MatDialog, MatDialogConfig } from '@angular/material/dialog';

import { AppConfig } from 'src/app/app.config';
import { countriesList } from 'src/app/utils/countries-list';

/**
 * Filters the user selected using SkysocksClientFilterComponent. It is prepopulated with default
 * data which indicates that no filter has been selected.
 */
export class SkysocksClientFilters {
  country = '';
  location = '';
  key = '';
}

/**
 * Data for SkysocksClientFilterComponent.
 */
export interface FilterWindowData {
  currentFilters: SkysocksClientFilters;
  availableCountries: string[];
}

/**
 * Modal window for selecting the filters for the elements list shown by
 * SkysocksClientSettingsComponent. If the user accepts the changes, the modal window is closed
 * and an instance of SkysocksClientFilters is returned in the "afterClosed" envent, with the
 * selected filters.
 */
@Component({
  selector: 'app-skysocks-client-filter',
  templateUrl: './skysocks-client-filter.component.html',
  styleUrls: ['./skysocks-client-filter.component.scss']
})
export class SkysocksClientFilterComponent implements OnInit {
  form: UntypedFormGroup;

  completeCountriesList = countriesList;

  /**
   * Opens the modal window. Please use this function instead of opening the window "by hand".
   */
  public static openDialog(dialog: MatDialog, data: FilterWindowData): MatDialogRef<SkysocksClientFilterComponent, any> {
    const config = new MatDialogConfig();
    config.data = data;
    config.autoFocus = false;
    config.width = AppConfig.smallModalWidth;

    return dialog.open(SkysocksClientFilterComponent, config);
  }

  constructor(
    @Inject(MAT_DIALOG_DATA) public data: FilterWindowData,
    public dialogRef: MatDialogRef<SkysocksClientFilterComponent>,
    private formBuilder: UntypedFormBuilder,
  ) { }

  ngOnInit() {
    // The '-' value is used when the country field is empty, to be able to show the "any" label,
    // due to the way in which Angular works.
    this.form = this.formBuilder.group({
      country: [this.data.currentFilters.country ? this.data.currentFilters.country : '-'],
      'location-text': [this.data.currentFilters.location],
      'key-text': [this.data.currentFilters.key],
    });
  }

  // Closes the modal window and returns the selected filters.
  apply() {
    const response = new SkysocksClientFilters();

    // If the value of the country field is '-', it means that no country was selected.
    let country = (this.form.get('country').value as string).trim();
    if (country === '-') {
      country = '';
    }

    response.country = country;
    response.location = (this.form.get('location-text').value as string).trim();
    response.key = (this.form.get('key-text').value as string).trim();

    this.dialogRef.close(response);
  }
}
