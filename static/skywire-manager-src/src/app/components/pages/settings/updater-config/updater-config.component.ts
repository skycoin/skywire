import { Component, OnInit, OnDestroy } from '@angular/core';
import { UntypedFormControl, UntypedFormGroup } from '@angular/forms';
import { MatDialog } from '@angular/material/dialog';
import { Subscription } from 'rxjs';

import { SnackbarService } from '../../../../services/snackbar.service';
import { UpdaterStorageKeys } from 'src/app/services/node.service';
import GeneralUtils from 'src/app/utils/generalUtils';

/**
 * Allows to set a custom configuration for the calls to the updater API endpoint.
 */
@Component({
  selector: 'app-updater-config',
  templateUrl: './updater-config.component.html',
  styleUrls: ['./updater-config.component.scss']
})
export class UpdaterConfigComponent implements OnInit, OnDestroy {
  form: UntypedFormGroup;

  // If there are custom settings saved in the app.
  hasCustomSettings: boolean;

  // Values currently saved in the app.
  private initialChannel: string;
  private initialVersion: string;
  private initialArchiveURL: string;
  private initialChecksumsURL: string;

  private subscription: Subscription;

  constructor(
    private snackbarService: SnackbarService,
    private dialog: MatDialog,
  ) { }

  ngOnInit() {
    // Get the currently saved data.
    this.initialChannel = localStorage.getItem(UpdaterStorageKeys.Channel);
    this.initialVersion = localStorage.getItem(UpdaterStorageKeys.Version);
    this.initialArchiveURL = localStorage.getItem(UpdaterStorageKeys.ArchiveURL);
    this.initialChecksumsURL = localStorage.getItem(UpdaterStorageKeys.ChecksumsURL);
    if (!this.initialChannel) {
      this.initialChannel = '';
    }
    if (!this.initialVersion) {
      this.initialVersion = '';
    }
    if (!this.initialArchiveURL) {
      this.initialArchiveURL = '';
    }
    if (!this.initialChecksumsURL) {
      this.initialChecksumsURL = '';
    }

    this.hasCustomSettings =
      !!this.initialChannel ||
      !!this.initialVersion ||
      !!this.initialArchiveURL ||
      !!this.initialChecksumsURL;

    this.form = new UntypedFormGroup({
      channel: new UntypedFormControl(this.initialChannel),
      version: new UntypedFormControl(this.initialVersion),
      archiveURL: new UntypedFormControl(this.initialArchiveURL),
      checksumsURL: new UntypedFormControl(this.initialChecksumsURL),
    });
  }

  ngOnDestroy() {
    if (this.subscription) {
      this.subscription.unsubscribe();
    }
  }

  // Allows to know if the user has modified the data in the form.
  get dataChanged(): boolean {
    return this.initialChannel !== (this.form.get('channel').value as string).trim() ||
      this.initialVersion !== (this.form.get('version').value as string).trim() ||
      this.initialArchiveURL !== (this.form.get('archiveURL').value as string).trim() ||
      this.initialChecksumsURL !== (this.form.get('checksumsURL').value as string).trim();
  }

  // Saves the settings entered in the form.
  saveSettings() {
    // Get the data entered in the form.
    const channel = (this.form.get('channel').value as string).trim();
    const version = (this.form.get('version').value as string).trim();
    const archiveURL = (this.form.get('archiveURL').value as string).trim();
    const checksumsURL = (this.form.get('checksumsURL').value as string).trim();

    if (channel || version || archiveURL || checksumsURL) {
      // Ask for confirmation.
      const confirmationDialog = GeneralUtils.createConfirmationDialog(this.dialog, 'settings.updater-config.save-confirmation');

      confirmationDialog.componentInstance.operationAccepted.subscribe(() => {
        confirmationDialog.close();

        // Save the data and update the form.
        this.initialChannel = channel;
        this.initialVersion = version;
        this.initialArchiveURL = archiveURL;
        this.initialChecksumsURL = checksumsURL;

        this.hasCustomSettings = true;

        localStorage.setItem(UpdaterStorageKeys.UseCustomSettings, 'true');
        localStorage.setItem(UpdaterStorageKeys.Channel, channel);
        localStorage.setItem(UpdaterStorageKeys.Version, version);
        localStorage.setItem(UpdaterStorageKeys.ArchiveURL, archiveURL);
        localStorage.setItem(UpdaterStorageKeys.ChecksumsURL, checksumsURL);

        this.snackbarService.showDone('settings.updater-config.saved');
      });
    } else {
      // If all fields are empty, erase the custom settings.
      this.removeSettings();
    }
  }

  // Removes the custom settings and cleans the form.
  removeSettings() {
    const confirmationDialog = GeneralUtils.createConfirmationDialog(this.dialog, 'settings.updater-config.remove-confirmation');

    confirmationDialog.componentInstance.operationAccepted.subscribe(() => {
      confirmationDialog.close();

      this.initialChannel = '';
      this.initialVersion = '';
      this.initialArchiveURL = '';
      this.initialChecksumsURL = '';

      this.form.get('channel').setValue('');
      this.form.get('version').setValue('');
      this.form.get('archiveURL').setValue('');
      this.form.get('checksumsURL').setValue('');

      this.hasCustomSettings = false;

      localStorage.removeItem(UpdaterStorageKeys.UseCustomSettings);
      localStorage.removeItem(UpdaterStorageKeys.Channel);
      localStorage.removeItem(UpdaterStorageKeys.Version);
      localStorage.removeItem(UpdaterStorageKeys.ArchiveURL);
      localStorage.removeItem(UpdaterStorageKeys.ChecksumsURL);

      this.snackbarService.showDone('settings.updater-config.removed');
    });
  }
}
