import { Component, OnInit, Inject } from '@angular/core';
import { FormBuilder, FormGroup } from '@angular/forms';
import { MAT_DIALOG_DATA, MatDialogRef, MatDialog, MatDialogConfig } from '@angular/material/dialog';

import { AppConfig } from 'src/app/app.config';

/**
 * Options for filtering by state.
 */
export enum StateFilterStates {
  NoFilter = 1,
  Available = 2,
  Offline = 3,
}

/**
 * Data for displaying the options for filtering by state.
 */
export interface StateFilter {
  /**
   * Text of the option, for using with the "translate" pipe.
   */
  text: string;
  /**
   * Value of the option.
   */
  state: StateFilterStates;
}

/**
 * Filters the user selected using SkysocksClientFilterComponent. It is prepopulated with default
 * data which indicates that no filter has been selected.
 */
export class SkysocksClientFilters {
  // Texts of the options for filtering by state.
  static readonly stateTexts = [
    'apps.skysocks-client-settings.filter-dialog.state-no-filter',
    'apps.skysocks-client-settings.state-available',
    'apps.skysocks-client-settings.state-offline',
  ];


  state: StateFilter = {
    text: SkysocksClientFilters.stateTexts[0],
    state: StateFilterStates.NoFilter
  };
  location = '';
  key = '';
}

/**
 * Modal window for selecting the filters for the proxy list shown by
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
  // Array with the options for filtering by state.
  stateFilters: StateFilter[];
  form: FormGroup;

  /**
   * Opens the modal window. Please use this function instead of opening the window "by hand".
   */
  public static openDialog(dialog: MatDialog, currentFilters: SkysocksClientFilters): MatDialogRef<SkysocksClientFilterComponent, any> {
    const config = new MatDialogConfig();
    config.data = currentFilters;
    config.autoFocus = false;
    config.width = AppConfig.smallModalWidth;

    return dialog.open(SkysocksClientFilterComponent, config);
  }

  constructor(
    @Inject(MAT_DIALOG_DATA) private data: SkysocksClientFilters,
    private dialogRef: MatDialogRef<SkysocksClientFilterComponent>,
    private formBuilder: FormBuilder,
  ) { }

  ngOnInit() {
    this.stateFilters = [
      {
        text: SkysocksClientFilters.stateTexts[0],
        state: StateFilterStates.NoFilter
      },
      {
        text: SkysocksClientFilters.stateTexts[1],
        state: StateFilterStates.Available
      },
      {
        text: SkysocksClientFilters.stateTexts[2],
        state: StateFilterStates.Offline
      }
    ];

    this.form = this.formBuilder.group({
      'state': [this.stateFilters[this.data.state.state - 1]],
      'location-text': [this.data.location],
      'key-text': [this.data.key],
    });
  }

  // Closes the modal window and returns the selected filters.
  apply() {
    const response = new SkysocksClientFilters();

    response.state = this.form.get('state').value;
    response.location = (this.form.get('location-text').value as string).trim();
    response.key = (this.form.get('key-text').value as string).trim();

    this.dialogRef.close(response);
  }
}
