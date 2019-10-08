import { Component, OnInit, ViewChild } from '@angular/core';
import { TransportService } from '../../../../../../services/transport.service';
import { NodeService } from '../../../../../../services/node.service';
import { FormBuilder, FormGroup, Validators } from '@angular/forms';
import { MatDialogRef, MatSnackBar } from '@angular/material';
import { TranslateService } from '@ngx-translate/core';
import { ButtonComponent } from '../../../../../layout/button/button.component';

@Component({
  selector: 'app-create-transport',
  templateUrl: './create-transport.component.html',
  styleUrls: ['./create-transport.component.css']
})
export class CreateTransportComponent implements OnInit {
  @ViewChild('button') button: ButtonComponent;
  types: string[];
  form: FormGroup;

  constructor(
    private nodeService: NodeService,
    private transportService: TransportService,
    private formBuilder: FormBuilder,
    private snackbar: MatSnackBar,
    private dialogRef: MatDialogRef<CreateTransportComponent>,
    private translate: TranslateService,
  ) { }

  ngOnInit() {
    this.transportService.types(this.nodeService.getCurrentNodeKey()).subscribe(types => this.types = types);

    this.form = this.formBuilder.group({
      'remoteKey': ['', Validators.compose([Validators.required, Validators.minLength(66), Validators.maxLength(66), Validators.pattern('^[0-9a-fA-F]+$')])],
      'type': ['', Validators.required],
    });
  }

  create() {
    if (! this.form.valid) {
      return;
    }

    this.button.loading();

    this.transportService.create(
      this.nodeService.getCurrentNodeKey(),
      this.form.get('remoteKey').value,
      this.form.get('type').value,
    )
      .subscribe(
        this.onSuccess.bind(this),
        this.onError.bind(this),
      );
  }

  private onSuccess() {
    this.snackbar.open(this.translate.instant('transports.dialog.success'));
    this.dialogRef.close();
  }

  private onError(error: string) {
    this.button.error(error);
    this.snackbar.open(this.translate.instant('transports.dialog.error', { error }));
  }
}
