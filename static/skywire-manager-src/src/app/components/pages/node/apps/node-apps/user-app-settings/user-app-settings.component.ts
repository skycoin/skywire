import { Component, OnInit, ViewChild, OnDestroy, Inject } from '@angular/core';
import { UntypedFormArray, UntypedFormBuilder, UntypedFormGroup } from '@angular/forms';
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
 * Modal window used for configuring user apps. It allows to add settings as name-value pairs
 */
@Component({
  selector: 'app-user-app-settings',
  templateUrl: './user-app-settings.component.html',
  styleUrls: ['./user-app-settings.component.scss']
})
export class UserAppSettingsComponent implements OnInit, OnDestroy {
  @ViewChild('button') button: ButtonComponent;

  form: UntypedFormGroup;

  appName = '';

  private operationSubscription: Subscription;

  /**
   * Opens the modal window. Please use this function instead of opening the window "by hand".
   */
  public static openDialog(dialog: MatDialog, app: Application): MatDialogRef<UserAppSettingsComponent, any> {
    const config = new MatDialogConfig();
    config.data = app;
    config.autoFocus = false;
    config.width = AppConfig.mediumModalWidth;

    return dialog.open(UserAppSettingsComponent, config);
  }

  constructor(
    @Inject(MAT_DIALOG_DATA) private data: Application,
    private appsService: AppsService,
    private formBuilder: UntypedFormBuilder,
    public dialogRef: MatDialogRef<UserAppSettingsComponent>,
    private snackbarService: SnackbarService,
    private dialog: MatDialog,
  ) {
    this.appName = data.name;
  }

  ngOnInit() {
    this.form = this.formBuilder.group({
      settings: this.formBuilder.array([]),
    });

    // Populate the form with the settings the app already has. This code asumes that the API
    // returns an array with the settings as pairs, where the setting name is in an element
    // position and the value is in the next one.
    if (this.data.args && this.data.args.length > 0) {
      for (let i = 0; i < this.data.args.length; i += 2) {
        if (i === this.data.args.length - 1) {
          break;
        }

        let name: string = this.data.args[i];
        const value: string = this.data.args[i + 1];

        // Remove the - symbol at the start of the name.
        name = name.startsWith('-') ? name.substring(1) : name;

        this.addSetting();

        const sc = this.settingsControls;
        sc[sc.length - 1].get('name').setValue(name);
        sc[sc.length - 1].get('value').setValue(value);
      }
    }
  }

  ngOnDestroy() {
    if (this.operationSubscription) {
      this.operationSubscription.unsubscribe();
    }
  }

  // Adds an entry to the settings list.
  addSetting() {
    const group = this.formBuilder.group({
      name: '',
      value: '',
    });

    (this.form.get('settings') as UntypedFormArray).push(group);
  }

  // Removes an entry from the settings list.
  removeSetting(index) {
    (this.form.get('settings') as UntypedFormArray).removeAt(index);
  }

  // Gets all the form field groups on the destinations array.
  get settingsControls() {
    return (this.form.get('settings') as UntypedFormArray).controls;
  }

  // If true, all the ways provided by default by the UI for closing the modal window are disabled.
  get disableDismiss(): boolean {
    return this.button ? this.button.isLoading : false;
  }

  // Saves the settings.
  saveChanges() {
    if (!this.form.valid || this.button.disabled) {
      return;
    }

    let hasEmpty = false;
    let added = 0;

    // Include only the settings with a name.
    const settings = {};
    this.settingsControls.forEach(st => {
      let name: string = st.get('name').value;
      let value: string = st.get('value').value;

      name = name ? name.trim() : name;
      value = value ? value.trim() : value;

      if (name) {
        settings[name] = value;
        added += 1;
      } else {
        hasEmpty = true;
      }
    });

    if (hasEmpty || added === 0) {
      // Ask for confirmation if the list is empty or if one or more settings were ignored.
      const confirmationMsg = 'apps.user-app-settings.' + (added === 0 ? 'empty-confirmation' : 'invalid-confirmation');
      const confirmationDialog = GeneralUtils.createConfirmationDialog(this.dialog, confirmationMsg);

      confirmationDialog.componentInstance.operationAccepted.subscribe(() => {
        confirmationDialog.close();
        this.continueSavingChanges(settings);
      });
    } else {
      this.continueSavingChanges(settings);
    }
  }

  // Performs the actual operation for saving the settings.
  private continueSavingChanges(settings: any) {
    this.button.showLoading();

    const data = { custom_setting: settings };

    this.operationSubscription = this.appsService.changeAppSettings(
      // The node pk is obtained from the currently openned node page.
      NodeComponent.getCurrentNodeKey(),
      this.data.name,
      data,
    ).subscribe({
      next: this.onSuccess.bind(this),
      error: this.onError.bind(this)
    });
  }

  private onSuccess() {
    NodeComponent.refreshCurrentDisplayedData();
    this.snackbarService.showDone('apps.user-app-settings.changes-made');
    this.dialogRef.close();
  }

  private onError(err: OperationError) {
    this.button.showError();
    err = processServiceError(err);

    this.snackbarService.showError(err);
  }
}
