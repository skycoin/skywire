import { Component, OnInit, Inject } from '@angular/core';
import { FormBuilder, FormGroup } from '@angular/forms';
import { MAT_DIALOG_DATA, MatDialogRef, MatDialog, MatDialogConfig } from '@angular/material/dialog';

import { AppConfig } from 'src/app/app.config';

/**
 * Filters the user selected using SkysocksClientFilterComponent. It is prepopulated with default
 * data which indicates that no filter has been selected.
 */
export class SkysocksClientFilters {
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
    this.form = this.formBuilder.group({
      'location-text': [this.data.location],
      'key-text': [this.data.key],
    });
  }

  // Closes the modal window and returns the selected filters.
  apply() {
    const response = new SkysocksClientFilters();

    response.location = (this.form.get('location-text').value as string).trim();
    response.key = (this.form.get('key-text').value as string).trim();

    this.dialogRef.close(response);
  }
}
