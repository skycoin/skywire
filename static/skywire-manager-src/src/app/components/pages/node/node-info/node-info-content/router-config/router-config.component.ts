import { Component, Inject, ViewChild, ElementRef, AfterViewInit, OnInit, OnDestroy } from '@angular/core';
import { MAT_DIALOG_DATA, MatDialogRef, MatDialogConfig, MatDialog } from '@angular/material/dialog';
import { FormGroup, FormBuilder, Validators } from '@angular/forms';
import { of, Subscription } from 'rxjs';

import { SnackbarService } from '../../../../../../services/snackbar.service';
import { AppConfig } from 'src/app/app.config';
import { Node } from '../../../../../../app.datatypes';
import { ButtonComponent } from 'src/app/components/layout/button/button.component';
import { delay } from 'rxjs/operators';
import { OperationError } from 'src/app/utils/operation-error';
import { processServiceError } from 'src/app/utils/errors';

/**
 * Modal window for changing the configuration related to the router. It changes the values
 * and shows a confirmation msg by itself.
 */
@Component({
  selector: 'app-router-config',
  templateUrl: './router-config.component.html',
  styleUrls: ['./router-config.component.scss']
})
export class RouterConfigComponent implements OnInit, OnDestroy, AfterViewInit {
  @ViewChild('button') button: ButtonComponent;
  @ViewChild('firstInput') firstInput: ElementRef;

  form: FormGroup;

  private operationSubscription: Subscription;

  /**
   * Opens the modal window. Please use this function instead of opening the window "by hand".
   */
  public static openDialog(dialog: MatDialog, node: Node): MatDialogRef<RouterConfigComponent, any> {
    const config = new MatDialogConfig();
    config.data = node;
    config.autoFocus = false;
    config.width = AppConfig.smallModalWidth;

    return dialog.open(RouterConfigComponent, config);
  }

  constructor(
    private dialogRef: MatDialogRef<RouterConfigComponent>,
    @Inject(MAT_DIALOG_DATA) private data: Node,
    private formBuilder: FormBuilder,
    private snackbarService: SnackbarService,
  ) { }

  ngOnInit() {
    this.form = this.formBuilder.group({
      'min': ['', Validators.compose([
        Validators.required,
        Validators.maxLength(3),
        Validators.pattern('^[0-9]+$')])],
      'max': ['', Validators.compose([
        Validators.required,
        Validators.maxLength(3),
        Validators.pattern('^[0-9]+$')])],
    });
  }

  ngAfterViewInit() {
    setTimeout(() => (this.firstInput.nativeElement as HTMLElement).focus());
  }

  ngOnDestroy() {
    if (this.operationSubscription) {
      this.operationSubscription.unsubscribe();
    }
  }

  save() {
    if (!this.form.valid || this.operationSubscription) {
      return;
    }

    this.button.showLoading();

    // TODO: change the values only if changes were made.

    this.operationSubscription = of(1).pipe(delay(1000)).subscribe({
      next: this.onSuccess.bind(this),
      error: this.onError.bind(this)
    });
  }

  private onSuccess(response: any) {
    this.dialogRef.close(true);
    this.snackbarService.showDone('router-config.done');
  }

  private onError(err: OperationError) {
    this.button.showError();
    this.operationSubscription = null;
    err = processServiceError(err);

    this.snackbarService.showError(err);
  }
}
