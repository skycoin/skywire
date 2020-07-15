import { Component, Inject } from '@angular/core';
import { MAT_DIALOG_DATA, MatDialog, MatDialogRef, MatDialogConfig } from '@angular/material/dialog';

import { AppConfig } from 'src/app/app.config';
import { FormGroup, FormBuilder } from '@angular/forms';

/**
 * Filters the user selected using TransportListComponent. It is prepopulated with default
 * data which indicates that no filter has been selected.
 */
export class TransportFilters {
  id = '';
  key = '';
}

/**
 * Modal window for selecting the filters for the transport list shown by
 * TransportListComponent. If the user accepts the changes, the modal window is closed
 * and an instance of TransportFilters is returned in the "afterClosed" envent, with the
 * selected filters.
 */
@Component({
  selector: 'app-transport-filters',
  templateUrl: './transport-filters.component.html',
  styleUrls: ['./transport-filters.component.scss']
})
export class TransportFiltersComponent {
  form: FormGroup;

  /**
   * Opens the modal window. Please use this function instead of opening the window "by hand".
   */
  public static openDialog(dialog: MatDialog, currentFilters: TransportFilters): MatDialogRef<TransportFiltersComponent, any> {
    const config = new MatDialogConfig();
    config.data = currentFilters;
    config.autoFocus = false;
    config.width = AppConfig.smallModalWidth;

    return dialog.open(TransportFiltersComponent, config);
  }

  constructor(
    @Inject(MAT_DIALOG_DATA) private data: TransportFilters,
    private dialogRef: MatDialogRef<TransportFiltersComponent>,
    private formBuilder: FormBuilder,
  ) { }

  ngOnInit() {
    this.form = this.formBuilder.group({
      'id-text': [this.data.id],
      'key-text': [this.data.key],
    });
  }

  // Closes the modal window and returns the selected filters.
  apply() {
    const response = new TransportFilters();

    response.id = (this.form.get('id-text').value as string).trim();
    response.key = (this.form.get('key-text').value as string).trim();

    this.dialogRef.close(response);
  }
}
