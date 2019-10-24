import { Component, Inject, Input, ViewChild, ElementRef, OnInit } from '@angular/core';
import { MAT_DIALOG_DATA, MatDialogRef } from '@angular/material';
import { FormGroup, FormBuilder } from '@angular/forms';

@Component({
  selector: 'app-edit-label',
  templateUrl: './edit-label.component.html',
  styleUrls: ['./edit-label.component.scss']
})
export class EditLabelComponent implements OnInit {
  @ViewChild('firstInput') firstInput: ElementRef;

  form: FormGroup;

  constructor(
    public dialogRef: MatDialogRef<EditLabelComponent>,
    @Inject(MAT_DIALOG_DATA) private data: any,
    private formBuilder: FormBuilder,
  ) { }

  ngOnInit() {
    this.form = this.formBuilder.group({
      'label': [this.data.label],
    });

    setTimeout(() => (this.firstInput.nativeElement as HTMLElement).focus());
  }

  save() {
    this.dialogRef.close(this.form.get('label').value);
  }
}
