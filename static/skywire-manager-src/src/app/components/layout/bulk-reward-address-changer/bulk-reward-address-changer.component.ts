import { Component, Inject, OnDestroy, ViewChild } from '@angular/core';
import { MatLegacyDialogRef as MatDialogRef, MAT_LEGACY_DIALOG_DATA as MAT_DIALOG_DATA, MatLegacyDialog as MatDialog, MatLegacyDialogConfig as MatDialogConfig } from '@angular/material/legacy-dialog';
import { Subscription, of, Observable } from 'rxjs';

import { AppConfig } from 'src/app/app.config';
import { NodeService } from 'src/app/services/node.service';
import { OperationError } from 'src/app/utils/operation-error';
import { FormArray, UntypedFormBuilder, UntypedFormGroup, Validators } from '@angular/forms';
import { ButtonComponent } from '../button/button.component';
import { delay, mergeMap } from 'rxjs/operators';
import GeneralUtils from 'src/app/utils/generalUtils';

/**
 * Params for BulkRewardAddressChangerComponent.
 */
export interface BulkRewardAddressParams {
  nodes: NodeToEditData[];
}

/**
 * Data about a node for BulkRewardAddressChangerComponent.
 */
export interface NodeToEditData {
  key: string;
  label: string;
}

/**
 * Extended data about a node, for internal use.
 */
interface NodeToEditCompleteData extends NodeToEditData {
  currentAddress?: string;
  operationError: string;
  processing: boolean;
}

/**
 * Modal window used for changing the rewards addresses of a list of nodes.
 */
@Component({
  selector: 'app-bulk-reward-address-changer',
  templateUrl: './bulk-reward-address-changer.component.html',
  styleUrls: ['./bulk-reward-address-changer.component.scss'],
})
export class BulkRewardAddressChangerComponent implements OnDestroy {
  @ViewChild('button') button: ButtonComponent;

  // If the process for changing the addresses has already started.
  processingStarted = false;
  // If the process for changing the addresses has already finished.
  processingFinished = false;
  // For how many nodes the procedure has already finished.
  currentlyProcessed = 0;

  // List with all the nodes that should be processed. At the start, it includes all nodes passed when the
  // window was opened. After the process starts, it only includes the nodes the user selected.
  nodesToEdit: NodeToEditCompleteData[];

  form: UntypedFormGroup;

  private operationSubscriptions: Subscription[];

  /**
   * Opens the modal window. Please use this function instead of opening the window "by hand".
   * @param nodes Nodes to process.
   */
  public static openDialog(dialog: MatDialog, nodes: BulkRewardAddressParams): MatDialogRef<BulkRewardAddressChangerComponent, any> {
    const config = new MatDialogConfig();
    config.data = nodes;
    config.autoFocus = false;
    config.width = AppConfig.smallModalWidth;

    return dialog.open(BulkRewardAddressChangerComponent, config);
  }

  constructor(
    public dialogRef: MatDialogRef<BulkRewardAddressChangerComponent>,
    @Inject(MAT_DIALOG_DATA) public data: BulkRewardAddressParams,
    private nodeService: NodeService,
    private formBuilder: UntypedFormBuilder,
    private dialog: MatDialog,
  ) {
    this.form = formBuilder.group({
      address: ['', Validators.compose([Validators.minLength(20), Validators.maxLength(40)])],
      nodes: formBuilder.array([]),
    });

    // Create an entry for each node, to let the user select and deselect nodes.
    data.nodes.forEach(n => {
      const nodeFormData = this.formBuilder.group({
        selected: [true],
      });
      (this.form.get('nodes') as FormArray).push(nodeFormData);
    });

    this.startChecking();
  }

  formValid(): boolean {
    if (!this.processingStarted) {
      if (!this.form.valid) {
        return false;
      }

      let selected = 0;
      (this.form.get('nodes') as FormArray).controls.forEach((n, i) => {
        if (n.get('selected')?.value) {
          selected += 1;
        }
      });

      return selected > 0;
    }

    return true;
  }

  /**
   * If true, all the ways provided by default by the UI for closing the modal window are disabled.
   */
  get disableDismiss(): boolean {
    return this.processingStarted && !this.processingFinished;
  }

  private startChecking() {
    // Create the list that will be shown on the UI.
    this.nodesToEdit = [];
    this.data.nodes.forEach(node => {
      this.nodesToEdit.push({
        key: node.key,
        label: node.label,
        currentAddress: null,
        operationError: '',
        processing: false,
      });
    });

    // Check the address currently configured on each node.
    this.operationSubscriptions = [];
    this.nodesToEdit.forEach((node, i) => {
      this.operationSubscriptions.push(
        this.nodeService.getRewardsAddress(node.key).subscribe(response => {
          this.nodesToEdit[i].currentAddress = response;
        }, (err: OperationError) => {
          this.nodesToEdit[i].operationError = err.translatableErrorMsg ? err.translatableErrorMsg : err.originalServerErrorMsg;
        })
      );
    });
  }

  /**
   * Checks the data entered by the user and ask for confirmation, if needed, before starting the
   * saving procedure.
   */
  checkBeforeProcessing() {
    if (!this.form.valid) {
      return;
    }

    const address = this.form.get('address').value as string;

    if (address) {
      this.startProcessing();
    } else {
      const confirmationDialog = GeneralUtils.createConfirmationDialog(this.dialog, 'bulk-rewards.empty-warning');
      confirmationDialog.componentInstance.operationAccepted.subscribe(() => {
        confirmationDialog.componentInstance.closeModal();
        this.startProcessing();
      });
    }
  }

  /**
   * Makes the changes on the selected nodes.
   */
  private startProcessing() {
    this.processingStarted = true;
    this.button.showLoading();

    this.closeoperationSubscriptions();

    // Remove the unselected nodes.
    const newList: NodeToEditCompleteData[] = [];
    (this.form.get('nodes') as FormArray).controls.forEach((n, i) => {
      if (n.get('selected')?.value) {
        this.nodesToEdit[i].operationError = '';
        this.nodesToEdit[i].processing = true;
        newList.push(this.nodesToEdit[i]);
      }
    });
    this.nodesToEdit = newList;

    const newAddress = this.form.get('address').value;
    this.form.get('address').disable();

    this.currentlyProcessed = 0;

    this.operationSubscriptions = [];
    this.nodesToEdit.forEach((node, i) => {
      // If the user entered an address, the operation must save it. If not, the operation is for
      // removing the currently saved address.
      let operation: Observable<any> = this.nodeService.setRewardsAddress(node.key, newAddress);
      if (!newAddress) {
        operation = this.nodeService.deleteRewardsAddress(node.key);
      }

      this.operationSubscriptions.push(
        of(0).pipe(delay(100), mergeMap(() => operation)).subscribe(response => {
          this.nodesToEdit[i].processing = false;

          this.currentlyProcessed += 1;
          if (this.currentlyProcessed === this.nodesToEdit.length) {
            this.processingFinished = true;
            this.button.reset();
          }

        }, (err: OperationError) => {
          this.nodesToEdit[i].processing = false;
          this.nodesToEdit[i].operationError = err.translatableErrorMsg ? err.translatableErrorMsg : err.originalServerErrorMsg;

          this.currentlyProcessed += 1;
          if (this.currentlyProcessed === this.nodesToEdit.length) {
            this.processingFinished = true;
            this.button.reset();
          }
        })
      );
    });
  }

  ngOnDestroy() {
    this.closeoperationSubscriptions();
  }

  private closeoperationSubscriptions() {
    if (this.operationSubscriptions) {
      this.operationSubscriptions.forEach(e => e.unsubscribe());
    }
  }

  closeModal() {
    this.dialogRef.close();
  }
}
