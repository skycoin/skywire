import { Component, Inject, Input, ViewChild, ElementRef, AfterViewInit, OnInit } from '@angular/core';
import { MAT_DIALOG_DATA, MatDialogRef } from '@angular/material/dialog';
import { FormGroup, FormBuilder } from '@angular/forms';
import { StorageService } from '../../../services/storage.service';
import { SnackbarService } from '../../../services/snackbar.service';

@Component({
  selector: 'app-edit-label',
  templateUrl: './edit-label.component.html',
  styleUrls: ['./edit-label.component.scss']
})
export class EditLabelComponent implements OnInit, AfterViewInit {
  @ViewChild('firstInput', { static: false }) firstInput: ElementRef;

  form: FormGroup;

  constructor(
    public dialogRef: MatDialogRef<EditLabelComponent>,
    @Inject(MAT_DIALOG_DATA) private data: any,
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
