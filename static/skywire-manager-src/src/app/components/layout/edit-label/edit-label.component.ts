import { Component, Inject, ViewChild, ElementRef, AfterViewInit, OnInit } from '@angular/core';
import { MAT_DIALOG_DATA, MatDialogRef, MatDialogConfig, MatDialog } from '@angular/material/dialog';
import { UntypedFormGroup, UntypedFormBuilder } from '@angular/forms';

import { StorageService, LabelInfo } from '../../../services/storage.service';
import { SnackbarService } from '../../../services/snackbar.service';
import { AppConfig } from 'src/app/app.config';

/**
 * Modal window for changing the label of a node. It changes the label and shows a confirmation
 * msg by itself.
 */
@Component({
  selector: 'app-edit-label',
  templateUrl: './edit-label.component.html',
  styleUrls: ['./edit-label.component.scss']
})
export class EditLabelComponent implements OnInit, AfterViewInit {
  @ViewChild('firstInput') firstInput: ElementRef;

  form: UntypedFormGroup;

  /**
   * Opens the modal window. Please use this function instead of opening the window "by hand".
   */
  public static openDialog(dialog: MatDialog, labelInfo: LabelInfo): MatDialogRef<EditLabelComponent, any> {
    const config = new MatDialogConfig();
    config.data = labelInfo;
    config.autoFocus = false;
    config.width = AppConfig.smallModalWidth;

    return dialog.open(EditLabelComponent, config);
  }

  constructor(
    public dialogRef: MatDialogRef<EditLabelComponent>,
    @Inject(MAT_DIALOG_DATA) private data: LabelInfo,
    private formBuilder: UntypedFormBuilder,
    private storageService: StorageService,
    private snackbarService: SnackbarService,
  ) { }

  ngOnInit() {
    this.form = this.formBuilder.group({
      label: [this.data.label],
    });
  }

  ngAfterViewInit() {
    setTimeout(() => (this.firstInput.nativeElement as HTMLElement).focus());
  }

  save() {
    const label = this.form.get('label').value.trim();

    // Save the data only if the label was changed.
    if (label !== this.data.label) {
      this.storageService.saveLabel(this.data.id, label, this.data.identifiedElementType);

      // This comprobation is used because sending an empty label to
      // storageService.saveLabel makes it to remove the label.
      if (!label) {
        this.snackbarService.showWarning('edit-label.label-removed-warning');
      } else {
        this.snackbarService.showDone('edit-label.done');
      }

      this.dialogRef.close(true);
    } else {
      this.dialogRef.close();
    }
  }
}
