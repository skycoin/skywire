import { Component, OnInit, ViewChild, OnDestroy, ElementRef, Inject } from '@angular/core';
import { FormBuilder, FormGroup, Validators } from '@angular/forms';
import { MatDialogRef, MatDialog, MatDialogConfig, MAT_DIALOG_DATA } from '@angular/material/dialog';
import { Subscription } from 'rxjs';

import { ButtonComponent } from '../../../../../layout/button/button.component';
import { NodeComponent } from '../../../node.component';
import { SnackbarService } from '../../../../../../services/snackbar.service';
import { AppConfig } from 'src/app/app.config';
import { processServiceError } from 'src/app/utils/errors';
import { OperationError } from 'src/app/utils/operation-error';
import { AppsService } from 'src/app/services/apps.service';
import GeneralUtils from 'src/app/utils/generalUtils';
import { Application } from 'src/app/app.datatypes';

/**
 * Modal window used for configuring the Skysocks-client app.
 */
@Component({
  selector: 'app-skysocks-client-settings',
  templateUrl: './skysocks-client-settings.component.html',
  styleUrls: ['./skysocks-client-settings.component.scss']
})
export class SkysocksClientSettingsComponent implements OnInit, OnDestroy {
  // Key for saving the history in persistent storage.
  private readonly historyStorageKey = 'SkysocksClientHistory';
  // Max elements the history can contain.
  readonly maxHistoryElements = 10;

  @ViewChild('button') button: ButtonComponent;
  @ViewChild('firstInput') firstInput: ElementRef;
  form: FormGroup;
  // Entries to show on the history.
  history: string[];

  // If the operation in being currently made.
  private working = false;
  // Last public key set to be sent to the backend.
  private lastPublicKey: string;
  private operationSubscription: Subscription;

  /**
   * Opens the modal window. Please use this function instead of opening the window "by hand".
   */
  public static openDialog(dialog: MatDialog, app: Application): MatDialogRef<SkysocksClientSettingsComponent, any> {
    const config = new MatDialogConfig();
    config.data = app;
    config.autoFocus = false;
    config.width = AppConfig.mediumModalWidth;

    return dialog.open(SkysocksClientSettingsComponent, config);
  }

  constructor(
    @Inject(MAT_DIALOG_DATA) private data: Application,
    private dialogRef: MatDialogRef<SkysocksClientSettingsComponent>,
    private appsService: AppsService,
    private formBuilder: FormBuilder,
    private snackbarService: SnackbarService,
    private dialog: MatDialog,
  ) { }

  ngOnInit() {
    // Get the history.
    const retrievedHistory = localStorage.getItem(this.historyStorageKey);
    if (retrievedHistory) {
      this.history = JSON.parse(retrievedHistory);
    } else {
      this.history = [];
    }

    // Get the current value saved on the visor, if it was returned by the API.
    let currentVal = '';
    if (this.data.args && this.data.args.length > 0) {
      for (let i = 0; i < this.data.args.length; i++) {
        if (this.data.args[i] === '-srv' && i + 1 < this.data.args.length) {
          currentVal = this.data.args[i + 1];
        }
      }
    }

    this.form = this.formBuilder.group({
      'pk': [currentVal, Validators.compose([
        Validators.required,
        Validators.minLength(66),
        Validators.maxLength(66),
        Validators.pattern('^[0-9a-fA-F]+$')])
      ],
    });

    setTimeout(() => (this.firstInput.nativeElement as HTMLElement).focus());
  }

  ngOnDestroy() {
    if (this.operationSubscription) {
      this.operationSubscription.unsubscribe();
    }
  }

  /**
   * Saves the settings.
   */
  saveChanges(publicKey: string = null) {
    if ((!this.form.valid && !publicKey) || this.working) {
      return;
    }

    this.lastPublicKey = publicKey ? publicKey : this.form.get('pk').value;

    // Ask for confirmation.
    const confirmationMsg = 'apps.skysocks-client-settings.change-key-confirmation';
    const confirmationDialog = GeneralUtils.createConfirmationDialog(this.dialog, confirmationMsg);
    confirmationDialog.componentInstance.operationAccepted.subscribe(() => {
      confirmationDialog.close();
      this.continueSavingChanges();
    });
  }

  private continueSavingChanges() {
    this.button.showLoading();
    this.working = true;

    this.operationSubscription = this.appsService.changeAppSettings(
      // The node pk is obtained from the currently openned node page.
      NodeComponent.getCurrentNodeKey(),
      this.data.name,
      { pk: this.lastPublicKey },
    ).subscribe({
      next: this.onSuccess.bind(this),
      error: this.onError.bind(this)
    });
  }

  private onSuccess() {
    // Remove any repeated entry from the history.
    this.history = this.history.filter(value => value !== this.lastPublicKey);

    // Save the new public key on the history.
    this.history = [this.lastPublicKey].concat(this.history);
    if (this.history.length > this.maxHistoryElements) {
      const itemsToRemove = this.history.length - this.maxHistoryElements;
      this.history.splice(this.history.length - itemsToRemove, itemsToRemove);
    }

    const dataToSave = JSON.stringify(this.history);
    localStorage.setItem(this.historyStorageKey, dataToSave);

    // Close the window.
    NodeComponent.refreshCurrentDisplayedData();
    this.snackbarService.showDone('apps.skysocks-client-settings.changes-made');
    this.dialogRef.close();
  }

  private onError(err: OperationError) {
    this.working = false;
    this.button.showError();
    err = processServiceError(err);

    this.snackbarService.showError(err);
  }
}
