import { Component, Inject, ViewChild, ElementRef, OnInit, OnDestroy } from '@angular/core';
import { MAT_DIALOG_DATA, MatDialogRef, MatDialogConfig, MatDialog } from '@angular/material/dialog';
import { UntypedFormGroup, UntypedFormBuilder, Validators } from '@angular/forms';
import { Observable, Subscription } from 'rxjs';

import { SnackbarService } from '../../../../../../services/snackbar.service';
import { AppConfig } from 'src/app/app.config';
import { ButtonComponent } from 'src/app/components/layout/button/button.component';
import { OperationError } from 'src/app/utils/operation-error';
import { processServiceError } from 'src/app/utils/errors';
import { NodeService } from 'src/app/services/node.service';
import GeneralUtils from 'src/app/utils/generalUtils';

/**
 * Params for RewardsAddressComponent.
 */
export interface RewardsAddressConfigParams {
  /**
   * PK of the node.
   */
  nodePk: string;
  /**
   * Current rewards address in the node.
   */
   currentAddress: string;
}

/**
 * Modal window for changing the rewards address of a node. It changes the values
 * and shows a confirmation msg by itself.
 */
@Component({
  selector: 'app-rewards-address-config',
  templateUrl: './rewards-address-config.component.html',
  styleUrls: ['./rewards-address-config.component.scss']
})
export class RewardsAddressComponent implements OnInit, OnDestroy {
  @ViewChild('button') button: ButtonComponent;
  @ViewChild('firstInput') firstInput: ElementRef;

  form: UntypedFormGroup;

  private operationSubscription: Subscription;

  /**
   * Opens the modal window. Please use this function instead of opening the window "by hand".
   */
  public static openDialog(dialog: MatDialog, node: RewardsAddressConfigParams): MatDialogRef<RewardsAddressComponent, any> {
    const config = new MatDialogConfig();
    config.data = node;
    config.autoFocus = false;
    config.width = AppConfig.smallModalWidth;

    return dialog.open(RewardsAddressComponent, config);
  }

  constructor(
    public dialogRef: MatDialogRef<RewardsAddressComponent>,
    @Inject(MAT_DIALOG_DATA) private data: RewardsAddressConfigParams,
    private formBuilder: UntypedFormBuilder,
    private snackbarService: SnackbarService,
    private nodeService: NodeService,
    private dialog: MatDialog,
  ) { }

  ngOnInit() {
    this.form = this.formBuilder.group({
      address: [this.data.currentAddress, Validators.compose([Validators.minLength(20), Validators.maxLength(40)])],
    });

    setTimeout(() => (this.firstInput.nativeElement as HTMLElement).focus());
  }

  ngOnDestroy() {
    if (this.operationSubscription) {
      this.operationSubscription.unsubscribe();
    }
  }

  /**
   * If true, all the ways provided by default by the UI for closing the modal window are disabled.
   */
  get disableDismiss(): boolean {
    return this.button ? this.button.isLoading : false;
  }

  /**
   * Checks the data entered by the user and ask for confirmation, if needed, before starting the
   * saving procedure.
   */
  startSaving() {
    if (!this.form.valid || this.operationSubscription) {
      return;
    }

    const address = this.form.get('address').value as string;

    if (address) {
      this.finishSaving();
    } else {
      const confirmationDialog = GeneralUtils.createConfirmationDialog(this.dialog, 'rewards-address-config.empty-warning');
      confirmationDialog.componentInstance.operationAccepted.subscribe(() => {
        confirmationDialog.componentInstance.closeModal();
        this.finishSaving();
      });
    }
  }

  // Makes the change on the back-end.
  private finishSaving() {
    this.button.showLoading();
    const newAddress = this.form.get('address').value;

    // If the user entered an address, the operation must save it. If not, the operation is for
    // removing the currently saved address.
    let operation: Observable<any> = this.nodeService.setRewardsAddress(this.data.nodePk, newAddress);
    if (!newAddress) {
      operation = this.nodeService.deleteRewardsAddress(this.data.nodePk);
    }

    this.operationSubscription = operation.subscribe({
      next: this.onSuccess.bind(this),
      error: this.onError.bind(this)
    });
  }

  private onSuccess(response: any) {
    this.dialogRef.close(true);
    this.snackbarService.showDone('rewards-address-config.done');
  }

  private onError(err: OperationError) {
    this.button.showError();
    this.operationSubscription = null;
    err = processServiceError(err);

    this.snackbarService.showError(err);
  }
}
