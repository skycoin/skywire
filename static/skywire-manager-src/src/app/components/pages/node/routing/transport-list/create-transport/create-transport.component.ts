import { Component, OnInit, ViewChild, OnDestroy, ElementRef } from '@angular/core';
import { TransportService } from '../../../../../../services/transport.service';
import { FormBuilder, FormGroup, Validators } from '@angular/forms';
import { MatDialogRef, MatSnackBar } from '@angular/material';
import { TranslateService } from '@ngx-translate/core';
import { ButtonComponent } from '../../../../../layout/button/button.component';
import { NodeComponent } from '../../../node.component';
import { Subscription, of } from 'rxjs';
import { delay, flatMap } from 'rxjs/operators';
import { ErrorsnackbarService } from '../../../../../../services/errorsnackbar.service';

@Component({
  selector: 'app-create-transport',
  templateUrl: './create-transport.component.html',
  styleUrls: ['./create-transport.component.css']
})
export class CreateTransportComponent implements OnInit, OnDestroy {
  @ViewChild('button') button: ButtonComponent;
  @ViewChild('firstInput') firstInput: ElementRef;
  types: string[];
  form: FormGroup;

  private shouldShowError = true;
  private dataSubscription: Subscription;

  constructor(
    private transportService: TransportService,
    private formBuilder: FormBuilder,
    private snackbar: MatSnackBar,
    private dialogRef: MatDialogRef<CreateTransportComponent>,
    private translate: TranslateService,
    private errorSnackBar: ErrorsnackbarService,
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
    this.dataSubscription.unsubscribe();
  }

  create() {
    if (! this.form.valid) {
      return;
    }

    this.button.loading();

    this.transportService.create(
      NodeComponent.getCurrentNodeKey(),
      this.form.get('remoteKey').value,
      this.form.get('type').value,
    ).subscribe(
      this.onSuccess.bind(this),
      this.onError.bind(this),
    );
  }

  private onSuccess() {
    NodeComponent.refreshCurrentDisplayedData();
    this.snackbar.open(this.translate.instant('transports.dialog.success'));
    this.dialogRef.close();
  }

  private onError(error: string) {
    this.button.error('');
    this.snackbar.open(this.translate.instant('transports.dialog.error', { error }));
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
        setTimeout(() => (this.firstInput.nativeElement as HTMLElement).focus());
        this.types = types;
      },
      () => {
        if (this.shouldShowError) {
          this.errorSnackBar.open(this.translate.instant('common.loading-error'));
          this.shouldShowError = false;
        }

        this.loadData(3000);
      },
    );
  }
}
