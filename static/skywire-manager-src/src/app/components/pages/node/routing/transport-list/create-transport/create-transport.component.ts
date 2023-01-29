import { Component, OnInit, ViewChild, OnDestroy, ElementRef } from '@angular/core';
import { UntypedFormBuilder, UntypedFormGroup, Validators } from '@angular/forms';
import { MatDialogRef, MatDialog, MatDialogConfig } from '@angular/material/dialog';
import { Subscription, of } from 'rxjs';
import { delay, flatMap } from 'rxjs/operators';

import { TransportService } from '../../../../../../services/transport.service';
import { ButtonComponent } from '../../../../../layout/button/button.component';
import { NodeComponent } from '../../../node.component';
import { SnackbarService } from '../../../../../../services/snackbar.service';
import { AppConfig } from 'src/app/app.config';
import { processServiceError } from 'src/app/utils/errors';
import { OperationError } from 'src/app/utils/operation-error';
import { LabeledElementTypes, StorageService } from 'src/app/services/storage.service';
import { NodeService } from 'src/app/services/node.service';
import { PersistentTransport } from 'src/app/app.datatypes';

/**
 * Modal window used for creating trnasports. It creates the transport and shows a
 * confirmation msg by itself.
 */
@Component({
  selector: 'app-create-transport',
  templateUrl: './create-transport.component.html',
  styleUrls: ['./create-transport.component.scss']
})
export class CreateTransportComponent implements OnInit, OnDestroy {
  @ViewChild('button') button: ButtonComponent;
  @ViewChild('firstInput') firstInput: ElementRef;
  types: string[];
  form: UntypedFormGroup;

  makePersistent = false;

  private shouldShowError = true;
  private dataSubscription: Subscription;
  private operationSubscription: Subscription;

  /**
   * Opens the modal window. Please use this function instead of opening the window "by hand".
   */
  public static openDialog(dialog: MatDialog): MatDialogRef<CreateTransportComponent, any> {
    const config = new MatDialogConfig();
    config.autoFocus = false;
    config.width = AppConfig.mediumModalWidth;

    return dialog.open(CreateTransportComponent, config);
  }

  constructor(
    private transportService: TransportService,
    private formBuilder: UntypedFormBuilder,
    public dialogRef: MatDialogRef<CreateTransportComponent>,
    private snackbarService: SnackbarService,
    private storageService: StorageService,
    private nodeService: NodeService,
  ) { }

  ngOnInit() {
    this.form = this.formBuilder.group({
      remoteKey: ['', Validators.compose([
        Validators.required,
        Validators.minLength(66),
        Validators.maxLength(66),
        Validators.pattern('^[0-9a-fA-F]+$')])
      ],
      label: [''],
      type: ['', Validators.required],
    });

    // Load the list of available types.
    this.loadData(0);
  }

  ngOnDestroy() {
    this.dataSubscription.unsubscribe();
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
   * Used by the checkbox for the persistent setting.
   */
   setMakePersistent(event) {
    this.makePersistent = event.checked ? true : false;
  }

  /**
   * Creates the transport.
   */
  create() {
    if (!this.form.valid || this.button.disabled) {
      return;
    }

    this.button.showLoading();

    const newTransportPk: string = this.form.get('remoteKey').value;
    const newTransportType: string = this.form.get('type').value;
    const newTransportLabel: string = this.form.get('label').value;

    if (this.makePersistent) {
      // Check the current visor config.
      const operation = this.transportService.getPersistentTransports(NodeComponent.getCurrentNodeKey());
      this.operationSubscription = operation.subscribe((list: any[]) => {
        const dataToUse = list ? list : [];

        let noNeedToAddToPersistents = false;

        // Check if the transport is already in the persistent list.
        dataToUse.forEach(t => {
          if (t.pk.toUpperCase() === newTransportPk.toUpperCase() && t.type.toUpperCase() === newTransportType.toUpperCase()) {
            noNeedToAddToPersistents = true;
          }
        });

        if (noNeedToAddToPersistents) {
          this.createTransport(newTransportPk, newTransportType, newTransportLabel, true);
        } else {
          this.createPersistent(dataToUse, newTransportPk, newTransportType, newTransportLabel);
        }
      }, err => {
        this.onError(err);
      });
    } else {
      this.createTransport(newTransportPk, newTransportType, newTransportLabel, false);
    }
  }

  /**
   * Updates the persistent transports list.
   * @param currentList Current persistent transports list.
   * @param newTransportPk Public key of the new transport.
   * @param newTransportType Type of the new transport.
   */
  private createPersistent(
    currentList: PersistentTransport[],
    newTransportPk: string,
    newTransportType: string,
    newTransportLabel: string
  ) {
    // Add the new transport.
    currentList.push({
      pk: newTransportPk,
      type: newTransportType,
    });

    this.operationSubscription = this.transportService.savePersistentTransportsData(
      NodeComponent.getCurrentNodeKey(),
      currentList
    ).subscribe(() => {
      this.createTransport(newTransportPk, newTransportType, newTransportLabel, true);
    }, err => {
      this.onError(err);
    });
  }

  /**
   * Creates a transport with the data entered in the form.
   * @param newTransportPk Public key of the new transport.
   * @param newTransportType Type of the new transport.
   */
  private createTransport(newTransportPk: string, newTransportType: string, newTransportLabel: string, creatingAfterPersistent: boolean) {
    this.operationSubscription = this.transportService.create(
      // The node pk is obtained from the currently openned node page.
      NodeComponent.getCurrentNodeKey(),
      newTransportPk,
      newTransportType,
    ).subscribe(response => {
      // Save the label.
      let errorSavingLabel = false;
      if (newTransportLabel) {
        if (response && response.id) {
          this.storageService.saveLabel(response.id, newTransportLabel, LabeledElementTypes.Transport);
        } else {
          errorSavingLabel = true;
        }
      }

      NodeComponent.refreshCurrentDisplayedData();
      this.dialogRef.close();

      if (!errorSavingLabel) {
        this.snackbarService.showDone('transports.dialog.success');
      } else {
        this.snackbarService.showWarning('transports.dialog.success-without-label');
      }
    }, err => {
      if (!creatingAfterPersistent) {
        this.onError(err);
      } else {
        NodeComponent.refreshCurrentDisplayedData();
        this.dialogRef.close();

        this.snackbarService.showWarning('transports.dialog.only-persistent-created');
      }
    });
  }

  private onError(err: OperationError) {
    this.button.showError();
    err = processServiceError(err);

    this.snackbarService.showError(err);
  }

  /**
   * Loads the list of available types.
   */
  private loadData(delayMilliseconds: number) {
    if (this.dataSubscription) {
      this.dataSubscription.unsubscribe();
    }

    this.dataSubscription = of(1).pipe(
      // Wait the delay.
      delay(delayMilliseconds),
      // Load the data. The node pk is obtained from the currently openned node page.
      flatMap(() => this.transportService.types(NodeComponent.getCurrentNodeKey()))
    ).subscribe(
      types => {
        // Sort the types and select dmsg as default, if posible.
        types.sort((a, b) => {
          // Put stcp at the end.
          if (a.toLowerCase() === 'stcp') {
            return 1;
          } else if (b.toLowerCase() === 'stcp') {
            return -1;
          }

          return a.localeCompare(b);
        });
        let defaultIndex = types.findIndex(type => type.toLowerCase() === 'dmsg');
        defaultIndex = defaultIndex !== -1 ? defaultIndex : 0;

        // Prepare the form.
        this.types = types;
        this.form.get('type').setValue(types[defaultIndex]);

        // Prepare the UI change.
        this.snackbarService.closeCurrentIfTemporaryError();
        setTimeout(() => (this.firstInput.nativeElement as HTMLElement).focus());
      },
      err => {
        err = processServiceError(err);

        // Show an error msg if it has not be done before during the current attempt to obtain the data.
        if (this.shouldShowError) {
          this.snackbarService.showError('common.loading-error', null, true, err);
          this.shouldShowError = false;
        }

        // Retry after a small delay.
        this.loadData(AppConfig.connectionRetryDelay);
      },
    );
  }
}
