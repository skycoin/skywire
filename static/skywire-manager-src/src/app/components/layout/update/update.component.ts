import { Component, Inject, OnDestroy, AfterViewInit, ChangeDetectorRef } from '@angular/core';
import { MatDialogRef, MAT_DIALOG_DATA, MatDialog, MatDialogConfig } from '@angular/material/dialog';
import { TranslateService } from '@ngx-translate/core';
import { Subscription, forkJoin, interval } from 'rxjs';

import { AppConfig } from 'src/app/app.config';
import { NodeService } from 'src/app/services/node.service';
import { StorageService } from 'src/app/services/storage.service';
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
 * Data about a node to update.
 */
export interface NodeData {
  key: string;
  label: string;
}

/**
 * Extended data about a node to update, for internal use.
 */
interface NodeToUpdate extends NodeData {
  /**
   * If there is an update for the node or it was detected as being updated, so the update
   * function must be called for it.
   */
  update: boolean;
  /**
   * Info about the current state of the update procedure.
   */
  updateProgressInfo: UpdateProgressInfo;
}

/**
 * Info about the current state of the update procedure of a node.
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
 * Modal window used for updating a list of nodes.
 */
@Component({
  selector: 'app-update',
  templateUrl: './update.component.html',
  styleUrls: ['./update.component.scss'],
})
export class UpdateComponent implements AfterViewInit, OnDestroy {
  // Current state of the window.
  state = UpdatingStates.InitialProcessing;

  // Text to show in the cancel button.
  cancelButtonText = 'common.cancel';
  // Text to show in the confirm button.
  confirmButtonText: string;
  // Error msg to show if the current state is UpdatingStates.Error.
  errorText: string;
  // If it was requested to update only one node and no updates were found, this var contains
  // the current version of the node.
  currentNodeVersion: string;

  // List with the names of all updates found for the nodes, without repeated values.
  updatesFound: UpdateVersion[];
  // List with all the nodes that should be updated. It includes all requested nodes, so it
  // may include nodes without updates available and nodes which are already being updated.
  nodesToUpdate: NodeToUpdate[];
  // List with the indexes, inside nodesToUpdate, of all nodes which were detected as already
  // being updated.
  indexesAlreadyBeingUpdated: number[] = [];
  // How many nodes inside nodesToUpdate have updates available and are not currently
  // being updated.
  nodesForUpdatesFound: number;

  updatingStates = UpdatingStates;

  private subscription: Subscription;
  private progressSubscriptions: Subscription[];
  private uiUpdateSubscription: Subscription;

  /**
   * Opens the modal window. Please use this function instead of opening the window "by hand".
   * @param nodes Nodes to update.
   */
  public static openDialog(dialog: MatDialog, nodes: NodeData[]): MatDialogRef<UpdateComponent, any> {
    const config = new MatDialogConfig();
    config.data = nodes;
    config.autoFocus = false;
    config.width = AppConfig.smallModalWidth;

    return dialog.open(UpdateComponent, config);
  }

  constructor(
    private dialogRef: MatDialogRef<UpdateComponent>,
    @Inject(MAT_DIALOG_DATA) public data: NodeData[],
    private nodeService: NodeService,
    private storageService: StorageService,
    private translateService: TranslateService,
    private changeDetectorRef: ChangeDetectorRef,
  ) { }

  ngAfterViewInit() {
    this.startChecking();
  }

  /**
   * Populates the nodesToUpdate property and starts checking which nodes are already
   * being updated.
   */
  private startChecking() {
    // Populate the nodesToUpdate list.
    this.nodesToUpdate = [];
    this.data.forEach(node => {
      this.nodesToUpdate.push({
        key: node.key,
        label: node.label ? node.label : this.storageService.getDefaultLabel(node.key),
        update: false,
        updateProgressInfo: new UpdateProgressInfo(),
      });

      this.nodesToUpdate[this.nodesToUpdate.length - 1].updateProgressInfo.rawMsg = this.translateService.instant('update.starting');
    });

    // Check which nodes are already being updated.
    this.subscription = forkJoin(this.data.map(node => this.nodeService.checkIfUpdating(node.key))).subscribe(nodesBeingUpdated => {
      // Save the list of nodes already being updated.
      nodesBeingUpdated.forEach((r, i) => {
        if (r.running) {
          this.indexesAlreadyBeingUpdated.push(i);
          this.nodesToUpdate[i].update = true;
        }
      });

      if (this.indexesAlreadyBeingUpdated.length === this.data.length) {
        // If all nodes are already being updated, call the update function for all of them and
        // start showing the progress.
        this.update();
      } else {
        // Continue to the next step.
        this.checkUpdates();
      }
    }, (err: OperationError) => {
      this.changeState(UpdatingStates.Error);
      this.errorText = processServiceError(err).translatableErrorMsg;
    });
  }

  /**
   * Checks if there are updates available for the nodes which are not currently being updated.
   */
  private checkUpdates() {
    this.nodesForUpdatesFound = 0;
    this.updatesFound = [];

    // Create a list with the nodes to check, ignoring the ones which are already being updated.
    const nodesToCheck: NodeToUpdate[] = [];
    this.nodesToUpdate.forEach(node => {
      if (!node.update) {
        nodesToCheck.push(node);
      }
    });

    // Check if there are updates.
    this.subscription = forkJoin(nodesToCheck.map(node => this.nodeService.checkUpdate(node.key))).subscribe(versionsResponse => {
      // Contains the list of all updates found, without repetitions.
      const updates = new Map<string, boolean>();

      // Check the response for each visor.
      versionsResponse.forEach((updateInfo, i) => {
        if (updateInfo && updateInfo.available) {
          // Mark the node for update.
          this.nodesForUpdatesFound += 1;
          nodesToCheck[i].update = true;

          // Save the name of the update, if it was not found before.
          if (!updates.has(updateInfo.current_version + updateInfo.available_version)) {
            this.updatesFound.push({
              currentVersion: updateInfo.current_version ?
                updateInfo.current_version : this.translateService.instant('common.unknown'),
              newVersion: updateInfo.available_version,
            });

            updates.set(updateInfo.current_version + updateInfo.available_version, true);
          }
        }
      });

      if (this.nodesForUpdatesFound > 0) {
        // If the procedure found updates, ask for confirmation before installing them.
        this.changeState(UpdatingStates.Asking);
      } else {
        // If no updates were found and there are no nodes currently being updated, show that
        // no updates were found.
        if (this.indexesAlreadyBeingUpdated.length === 0) {
          this.changeState(UpdatingStates.NoUpdatesFound);

          if (this.data.length === 1) {
            this.currentNodeVersion = versionsResponse[0].current_version;
          }
        } else {
          // Continue to the update function to show the progress of the nodes which
          // are currently being updated.
          this.update();
        }
      }
    }, (err: OperationError) => {
      this.changeState(UpdatingStates.Error);
      this.errorText = processServiceError(err).translatableErrorMsg;
    });
  }

  /**
   * Calls the update API endpoint for all the nodes in the nodesToUpdate list with
   * update === true. This makes the update procedure to start, if it was not already
   * started and starts showing the progress.
   */
  private update() {
    this.changeState(UpdatingStates.Updating);

    this.progressSubscriptions = [];
    this.nodesToUpdate.forEach((nodeToUpdate, i) => {
      if (nodeToUpdate.update) {
        // Start the update procedure.
        this.progressSubscriptions.push(
          this.nodeService.update(nodeToUpdate.key).subscribe(response => {
            // Update the progress.
            this.updateProgressInfo(response.status, nodeToUpdate.updateProgressInfo);
          }, (err: OperationError) => {
            // Save the error msg.
            nodeToUpdate.updateProgressInfo.errorMsg = processServiceError(err).translatableErrorMsg;
          }, () => {
            // Indicate that the connection has been closed.
            nodeToUpdate.updateProgressInfo.closed = true;
          })
        );
      }
    });
  }

  /**
   * Returns the translatable var that must be used before the list of updates found.
   */
  get updateAvailableText(): string {
    if (this.data.length === 1) {
      // If only one node was requested to be updated.
      return 'update.update-available';
    } else {
      // If more than one node was requested to be updated, build the var taking into
      // account how many nodes will be updated and if there are nodes already being updated.
      let response = 'update.update-available';

      if (this.indexesAlreadyBeingUpdated.length > 0) {
        response += '-additional';
      }

      if (this.nodesForUpdatesFound === 1) {
        response += '-singular';
      } else {
        response += '-plural';
      }

      return response;
    }
  }

  /**
   * Tries to parse a response returned by the backend and updates the values of an
   * UpdateProgressInfo instance with the info it was able to recover.
   * @param progressMsg Response returned by the backend.
   * @param infoToUpdate Instance to update.
   */
  private updateProgressInfo(progressMsg: string, infoToUpdate: UpdateProgressInfo) {
    // Save basic data.
    infoToUpdate.rawMsg = progressMsg;
    infoToUpdate.dataParsed = false;

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
            infoToUpdate.fileName = progressMsg.substring(initialFileIndex, finalFileIndex);
          } else {
            errorFound = true;
          }
        }

        if (!errorFound) {
          infoToUpdate.speed = progressMsg.substring(initialSpeedIndex + 1, finalSpeedIndex);
          infoToUpdate.elapsedTime = progressMsg.substring(initialTimeIndex + 1, timeSeparatorIndex);
          infoToUpdate.remainingTime = progressMsg.substring(timeSeparatorIndex + 1, finalTimeIndex);

          const initialProgressIndex = progressMsg.lastIndexOf(' ', progressPercentageIndex);
          infoToUpdate.progress = Number(progressMsg.substring(initialProgressIndex + 1, progressPercentageIndex));
        }
      } catch (e) {
        errorFound = true;
      }

      if (!errorFound) {
        // Indicate that the response was corrently parsed only if all data was obtained.
        infoToUpdate.dataParsed = true;
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

    if (this.progressSubscriptions) {
      this.progressSubscriptions.forEach(e => e.unsubscribe());
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
      this.confirmButtonText = 'update.install';
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
