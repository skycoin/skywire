import { Component, Inject, ViewChild, ElementRef, AfterViewInit, OnInit } from '@angular/core';
import { MAT_DIALOG_DATA, MatDialogRef, MatDialogConfig, MatDialog } from '@angular/material/dialog';
import { FormGroup, FormBuilder } from '@angular/forms';
import { StorageService } from '../../../services/storage.service';
import { SnackbarService } from '../../../services/snackbar.service';
import { Node } from '../../../app.datatypes';
import { AppConfig } from 'src/app/app.config';

@Component({
  selector: 'app-edit-label',
  templateUrl: './edit-label.component.html',
  styleUrls: ['./edit-label.component.scss']
})
export class EditLabelComponent implements OnInit, AfterViewInit {
  @ViewChild('firstInput', { static: false }) firstInput: ElementRef;

  form: FormGroup;

  public static openDialog(dialog: MatDialog, data: Node): MatDialogRef<EditLabelComponent, any> {
    const config = new MatDialogConfig();
    config.data = data;
    config.autoFocus = false;
    config.width = AppConfig.smallModalWidth;

    return dialog.open(EditLabelComponent, config);
  }

  constructor(
    public dialogRef: MatDialogRef<EditLabelComponent>,
    @Inject(MAT_DIALOG_DATA) private data: Node,
    private formBuilder: FormBuilder,
    private storageService: StorageService,
    private snackbarService: SnackbarService,
  ) { }

  ngOnInit() {
    this.form = this.formBuilder.group({
      'label': [this.data.label],
    });
  }

  ngAfterViewInit() {
    setTimeout(() => (this.firstInput.nativeElement as HTMLElement).focus());
  }

  save() {
    const label = this.form.get('label').value.trim();
    this.storageService.setNodeLabel(this.data.local_pk, label);

    if (!label) {
      this.snackbarService.showWarning('edit-label.default-label-warning');
    } else {
      this.snackbarService.showDone('edit-label.done');
    }

    this.dialogRef.close(true);
  }
}
