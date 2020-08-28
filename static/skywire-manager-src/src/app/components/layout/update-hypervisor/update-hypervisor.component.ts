import { Component, OnDestroy, AfterViewInit, ChangeDetectorRef } from '@angular/core';
import { MatDialogRef, MatDialog, MatDialogConfig } from '@angular/material/dialog';
import { TranslateService } from '@ngx-translate/core';
import { Subscription, interval } from 'rxjs';

import { AppConfig } from 'src/app/app.config';
import { NodeService } from 'src/app/services/node.service';
import { OperationError } from 'src/app/utils/operation-error';
import { processServiceError } from 'src/app/utils/errors';

/**
 * States of the modal window.
 */
enum UpdatingStates {
  /**
   * Looking for updates.
   */
  InitialProcessing = 'InitialProcessing',
  /**
   * If no update was found.
   */
  NoUpdatesFound = 'NoUpdatesFound',
  /**
   * Showing the list of updates found and asking for confirmation before installing them.
   */
  Asking = 'Asking',
  /**
   * Installing the updates.
   */
  Updating = 'Updating',
  /**
   * Showing an error msg. Operation cancelled.
   */
  Error = 'Error',
}

/**
 * Info about the current state of the update procedure.
 */
export class UpdateProgressInfo {
  /**
   * Error found while updating. If it has a valid value, the whole procedure must be
   * considered as failed.
   */
  errorMsg = '';
  /**
   * Raw progress text obtained from the backend.
   */
  rawMsg = '';
  /**
   * If it was posible to parse the raw progress text obtained from the backend and
   * populate the rest of the vars.
   */
  dataParsed = false;
  /**
   * Name of the file currently being downloaded (only is dataParsed === true).
   */
  fileName = '';
  /**
   * Progress downloading the file, in percentage (only is dataParsed === true).
   */
  progress = 100;
  /**
   * Current download speed (only is dataParsed === true).
   */
  speed = '';
  /**
   * Time since starting to download the file (only is dataParsed === true).
   */
  elapsedTime = '';
  /**
   * Expected time for finishing to download the file (only is dataParsed === true).
   */
  remainingTime = '';
  /**
   * If true, the connection with the backend for getting progress updates has been clo0sed.
   */
  closed = false;
}

/**
 * Data about an update found.
 */
interface UpdateVersion {
  currentVersion: string;
  newVersion: string;
}

/**
 * Modal window used for updating the hypervisor. NOTE: this is a copy of UpdateComponent with
 * the changes needed for updating the hypervisor instead of a node. As the hypervisor is going
 * to be integration in the visors in the near future, this component will no longer be needed.
 */
@Component({
  selector: 'app-update-hypervisor',
  templateUrl: './update-hypervisor.component.html',
  styleUrls: ['./update-hypervisor.component.scss'],
})
export class UpdateHypervisorComponent implements AfterViewInit, OnDestroy {
  // Current state of the window.
  state = UpdatingStates.InitialProcessing;

  // Text to show in the cancel button.
  cancelButtonText = 'common.cancel';
  // Text to show in the confirm button.
  confirmButtonText: string;
  // Error msg to show if the current state is UpdatingStates.Error.
  errorText: string;
  // If no updates were found, this var contains the current version of the hypervisor.
  currentVersion: string;

  progressInfo = new UpdateProgressInfo();

  // Update found.
  updateFound: UpdateVersion;

  updatingStates = UpdatingStates;

  private subscription: Subscription;
  private progressSubscription: Subscription;
  private uiUpdateSubscription: Subscription;

  /**
   * Opens the modal window. Please use this function instead of opening the window "by hand".
   */
  public static openDialog(dialog: MatDialog): MatDialogRef<UpdateHypervisorComponent, any> {
    const config = new MatDialogConfig();
    config.autoFocus = false;
    config.width = AppConfig.smallModalWidth;

    return dialog.open(UpdateHypervisorComponent, config);
  }

  constructor(
    private dialogRef: MatDialogRef<UpdateHypervisorComponent>,
    private nodeService: NodeService,
    private translateService: TranslateService,
    private changeDetectorRef: ChangeDetectorRef,
  ) { }

  ngAfterViewInit() {
    this.startChecking();
  }

  /**
   * Checks if the hypervisor is already being updated.
   */
  private startChecking() {
    // NOTE: This code is needed for checking if the hypervisor is currently being updated, but
    // is commented due to a bug with the Hypervisor API. Must be reactivated, and the last
    // cuerrently uncommented line deleted, after the bug is solved.
    /*
    this.subscription = this.nodeService.checkIfUpdating(null).subscribe(result => {
      if (result.running) {
        this.update();
      } else {
        this.checkUpdates();
      }
    }, (err: OperationError) => {
      this.changeState(UpdatingStates.Error);
      this.errorText = processServiceError(err).translatableErrorMsg;
    });
    */
    this.checkUpdates();
  }

  /**
   * Checks if there are updates available for the hypervisor.
   */
  private checkUpdates() {
    this.subscription = this.nodeService.checkUpdate(null).subscribe(result => {
      if (result && result.available) {
        // Save the update.
        this.updateFound = {
          currentVersion: result.current_version ?
          result.current_version : this.translateService.instant('common.unknown'),
          newVersion: result.available_version,
        };

        // Go to the next step.
        this.changeState(UpdatingStates.Asking);
      } else {
        this.changeState(UpdatingStates.NoUpdatesFound);
      }
    }, (err: OperationError) => {
      this.changeState(UpdatingStates.Error);
      this.errorText = processServiceError(err).translatableErrorMsg;
    });
  }

  /**
   * Calls the update API endpoint for the hypervisor. This makes the update procedure to start,
   * if it was not already started, and starts showing the progress.
   */
  update() {
    this.changeState(UpdatingStates.Updating);

    this.progressInfo.rawMsg = this.translateService.instant('update.starting');

    this.progressSubscription = this.nodeService.update(null).subscribe(response => {
      // Update the progress.
      this.updateProgressInfo(response.status);
    }, (err: OperationError) => {
      // Save the error msg.
      this.progressInfo.errorMsg = processServiceError(err).translatableErrorMsg;
    }, () => {
      // Indicate that the connection has been closed.
      this.progressInfo.closed = true;
    });
  }

  /**
   * Tries to parse a response returned by the backend and updates the values of
   * this.progressInfo with the info it was able to recover.
   * @param progressMsg Response returned by the backend.
   */
  private updateProgressInfo(progressMsg: string) {
    // Save basic data.
    this.progressInfo.rawMsg = progressMsg;
    this.progressInfo.dataParsed = false;

    // Try to get the indexes of parts which are expected to be found in the response.
    const downloadingIndex = progressMsg.indexOf('Downloading');
    const initialSpeedIndex = progressMsg.lastIndexOf('(');
    const finalSpeedIndex = progressMsg.lastIndexOf(')');
    const initialTimeIndex = progressMsg.lastIndexOf('[');
    const finalTimeIndex = progressMsg.lastIndexOf(']');
    const timeSeparatorIndex = progressMsg.lastIndexOf(':');
    const progressPercentageIndex = progressMsg.lastIndexOf('%');

    // Continue only if all indexes were found.
    if (
      downloadingIndex !== -1 &&
      initialSpeedIndex !== -1 &&
      finalSpeedIndex !== -1 &&
      initialTimeIndex !== -1 &&
      finalTimeIndex !== -1 &&
      timeSeparatorIndex !== -1
    ) {
      // Additional security checks.
      let errorFound = false;
      if (initialSpeedIndex > finalSpeedIndex) {
        errorFound = true;
      }
      if (initialTimeIndex > timeSeparatorIndex) {
        errorFound = true;
      }
      if (timeSeparatorIndex > finalTimeIndex) {
        errorFound = true;
      }
      if (progressPercentageIndex > initialSpeedIndex || progressPercentageIndex < downloadingIndex) {
        errorFound = true;
      }

      // Try to get all the data.
      try {
        if (!errorFound) {
          const initialFileIndex = downloadingIndex + 'Downloading'.length + 1;
          const finalFileIndex = progressMsg.indexOf(' ', initialFileIndex);

          if (initialFileIndex !== -1 && finalFileIndex !== -1) {
            this.progressInfo.fileName = progressMsg.substring(initialFileIndex, finalFileIndex);
          } else {
            errorFound = true;
          }
        }

        if (!errorFound) {
          this.progressInfo.speed = progressMsg.substring(initialSpeedIndex + 1, finalSpeedIndex);
          this.progressInfo.elapsedTime = progressMsg.substring(initialTimeIndex + 1, timeSeparatorIndex);
          this.progressInfo.remainingTime = progressMsg.substring(timeSeparatorIndex + 1, finalTimeIndex);

          const initialProgressIndex = progressMsg.lastIndexOf(' ', progressPercentageIndex);
          this.progressInfo.progress = Number(progressMsg.substring(initialProgressIndex + 1, progressPercentageIndex));
        }
      } catch (e) {
        errorFound = true;
      }

      if (!errorFound) {
        // Indicate that the response was corrently parsed only if all data was obtained.
        this.progressInfo.dataParsed = true;
      }
    }
  }

  ngOnDestroy() {
    if (this.subscription) {
      this.subscription.unsubscribe();
    }
    if (this.uiUpdateSubscription) {
      this.uiUpdateSubscription.unsubscribe();
    }
    if (this.progressSubscription) {
      this.progressSubscription.unsubscribe();
    }
  }

  closeModal() {
    this.dialogRef.close();
  }

  /**
   * Changes the current state of the window. Depending on the new state, it updates some other
   * properties to ensure the new state is correctly shown.
   */
  private changeState(newState: UpdatingStates) {
    this.state = newState;

    // Update the buttons depending on the new state.
    if (newState === UpdatingStates.Error) {
      this.confirmButtonText = 'common.close';
      this.cancelButtonText = '';
    } else if (newState === UpdatingStates.Asking) {
      this.confirmButtonText = 'update-hypervisor.install';
      this.cancelButtonText = 'common.cancel';
    } else if (newState === UpdatingStates.NoUpdatesFound) {
      this.confirmButtonText = 'common.close';
      this.cancelButtonText = '';
    } else if (newState === UpdatingStates.Updating) {
      this.confirmButtonText = 'common.close';
      this.cancelButtonText = '';

      // Ensure the changes in the properties are shown in the UI periodically.
      this.uiUpdateSubscription = interval(1000).subscribe(() => this.changeDetectorRef.detectChanges());
    }
  }
}
