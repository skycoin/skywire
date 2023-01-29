import { Component, ViewChild, ElementRef, OnInit, Inject } from '@angular/core';
import { MatDialogRef, MatDialogConfig, MatDialog, MAT_DIALOG_DATA } from '@angular/material/dialog';
import { UntypedFormGroup, UntypedFormBuilder } from '@angular/forms';

import { AppConfig } from 'src/app/app.config';

/**
 * Modal window for changing the note of an entry shown on SkysocksClientSettingsComponent.
 * If the user selects the option for saving the note, the modal window is closed and the new
 * note is returned in the "afterClosed" envent, but with an hyphen "-" added to the begining,
 * to help avoiding problems while checking empty strings.
 */
@Component({
  selector: 'app-edit-skysocks-client-note',
  templateUrl: './edit-skysocks-client-note.component.html',
  styleUrls: ['./edit-skysocks-client-note.component.scss']
})
export class EditSkysocksClientNoteComponent implements OnInit {
  @ViewChild('firstInput', { static: false }) firstInput: ElementRef;

  form: UntypedFormGroup;

  /**
   * Opens the modal window. Please use this function instead of opening the window "by hand".
   */
  public static openDialog(dialog: MatDialog, currentNote: string): MatDialogRef<EditSkysocksClientNoteComponent, any> {
    const config = new MatDialogConfig();
    config.data = currentNote ? currentNote : '';
    config.autoFocus = false;
    config.width = AppConfig.smallModalWidth;

    return dialog.open(EditSkysocksClientNoteComponent, config);
  }

  constructor(
    public dialogRef: MatDialogRef<EditSkysocksClientNoteComponent>,
    @Inject(MAT_DIALOG_DATA) private data: string,
    private formBuilder: UntypedFormBuilder,
  ) { }

  ngOnInit() {
    this.form = this.formBuilder.group({
      note: [this.data],
    });

    setTimeout(() => (this.firstInput.nativeElement as HTMLElement).focus());
  }

  // Closes the modal window and returns the note.
  finish() {
    const note = this.form.get('note').value.trim();
    this.dialogRef.close('-' + note);
  }
}
