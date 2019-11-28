import { Component, OnInit, ViewChild, OnDestroy, ElementRef } from '@angular/core';
import { TransportService } from '../../../../../../services/transport.service';
import { FormBuilder, FormGroup, Validators } from '@angular/forms';
import { MatDialogRef, MatDialog, MatDialogConfig } from '@angular/material/dialog';
import { MatSnackBar } from '@angular/material/snack-bar';
import { ButtonComponent } from '../../../../../layout/button/button.component';
import { NodeComponent } from '../../../node.component';
import { Subscription, of } from 'rxjs';
import { delay, flatMap } from 'rxjs/operators';
import { SnackbarService } from '../../../../../../services/snackbar.service';
import { AppConfig } from 'src/app/app.config';

@Component({
  selector: 'app-create-transport',
  templateUrl: './create-transport.component.html',
  styleUrls: ['./create-transport.component.css']
})
export class CreateTransportComponent implements OnInit, OnDestroy {
  @ViewChild('button', { static: false }) button: ButtonComponent;
  @ViewChild('firstInput', { static: false }) firstInput: ElementRef;
  types: string[];
  form: FormGroup;

  private shouldShowError = true;
  private dataSubscription: Subscription;

  public static openDialog(dialog: MatDialog): MatDialogRef<CreateTransportComponent, any> {
    const config = new MatDialogConfig();
    config.autoFocus = false;
    config.width = AppConfig.mediumModalWidth;

    return dialog.open(CreateTransportComponent, config);
  }

  constructor(
    private transportService: TransportService,
    private formBuilder: FormBuilder,
    private snackbar: MatSnackBar,
    private dialogRef: MatDialogRef<CreateTransportComponent>,
    private snackbarService: SnackbarService,
  ) { }

  ngOnInit() {
    this.form = this.formBuilder.group({
      'remoteKey': ['', Validators.compose([
        Validators.required,
        Validators.minLength(66),
        Validators.maxLength(66),
        Validators.pattern('^[0-9a-fA-F]+$')])
      ],
      'type': ['', Validators.required],
    });

    this.loadData(0);
  }

  ngOnDestroy() {
    this.snackbarService.closeCurrentIfTemporalError();
    this.dataSubscription.unsubscribe();
  }

  create() {
    if (! this.form.valid) {
      return;
    }

    this.button.showLoading();

    this.transportService.create(
      NodeComponent.getCurrentNodeKey(),
      this.form.get('remoteKey').value,
      this.form.get('type').value,
    ).subscribe({
      next: this.onSuccess.bind(this),
      error: this.onError.bind(this)
    });
  }

  private onSuccess() {
    NodeComponent.refreshCurrentDisplayedData();
    this.snackbarService.showDone('transports.dialog.success');
    this.dialogRef.close();
  }

  private onError(error: string) {
    this.button.showError();
    this.snackbarService.showError('transports.dialog.error');
  }

  private loadData(delayMilliseconds: number) {
    if (this.dataSubscription) {
      this.dataSubscription.unsubscribe();
    }

    this.dataSubscription = of(1).pipe(
      delay(delayMilliseconds),
      flatMap(() => this.transportService.types(NodeComponent.getCurrentNodeKey()))
    ).subscribe(
      types => {
        this.snackbarService.closeCurrentIfTemporalError();
        setTimeout(() => (this.firstInput.nativeElement as HTMLElement).focus());
        this.types = types;
      },
      () => {
        if (this.shouldShowError) {
          this.snackbarService.showError('common.loading-error', null, true);
          this.shouldShowError = false;
        }

        this.loadData(3000);
      },
    );
  }
}
