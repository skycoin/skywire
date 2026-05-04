import { Component, OnInit, ViewChild, OnDestroy, Inject } from '@angular/core';
import { UntypedFormBuilder, UntypedFormGroup, Validators } from '@angular/forms';
import { MatDialogRef, MatDialog, MatDialogConfig, MAT_DIALOG_DATA } from '@angular/material/dialog';
import { Subscription } from 'rxjs';

import { ButtonComponent } from '../../../../../layout/button/button.component';
import { NodeComponent } from '../../../node.component';
import { SnackbarService } from '../../../../../../services/snackbar.service';
import { AppConfig } from 'src/app/app.config';
import { processServiceError } from 'src/app/utils/errors';
import { OperationError } from 'src/app/utils/operation-error';
import { AppsService } from 'src/app/services/apps.service';
import { Application } from 'src/app/app.datatypes';
import GeneralUtils from 'src/app/utils/generalUtils';

/**
 * Modal window used for configuring the Skychat app. Mirrors the
 * `skywire app skychat --help` flag set:
 *   --addr / --port  → port + localhost-only toggle
 *   --skynet / --dmsg → network listener toggles
 *   --persist + --persist-* → persistence section
 *   --pair-enable     → pairing section
 *
 * Filesystem-path flags (--persist-db, --persist-whitelist) are
 * intentionally omitted — those should be configured on the box
 * (the visor doesn't validate the path is reachable in its sandbox).
 */
@Component({
    selector: 'app-skychat-settings',
    templateUrl: './skychat-settings.component.html',
    styleUrls: ['./skychat-settings.component.scss'],
    standalone: false
})
export class SkychatSettingsComponent implements OnInit, OnDestroy {
  @ViewChild('button') button: ButtonComponent;
  form: UntypedFormGroup;

  private formSubscription: Subscription;
  private operationSubscription: Subscription;

  /**
   * Opens the modal window. Please use this function instead of opening the window "by hand".
   */
  public static openDialog(dialog: MatDialog, app: Application): MatDialogRef<SkychatSettingsComponent, any> {
    const config = new MatDialogConfig();
    config.data = app;
    config.autoFocus = false;
    config.width = AppConfig.mediumModalWidth;

    return dialog.open(SkychatSettingsComponent, config);
  }

  constructor(
    @Inject(MAT_DIALOG_DATA) private data: Application,
    private appsService: AppsService,
    private formBuilder: UntypedFormBuilder,
    public dialogRef: MatDialogRef<SkychatSettingsComponent>,
    private snackbarService: SnackbarService,
    private dialog: MatDialog,
  ) { }

  ngOnInit() {
    this.form = this.formBuilder.group({
      // Listener.
      localhostOnly: [true],
      port: ['', Validators.compose([Validators.required, Validators.min(1025), Validators.max(65536)])],
      // Network toggles.
      skynet: [true],
      dmsg: [true],
      // Persistence.
      persist: [false],
      persistMaxSize: ['', Validators.pattern('^[0-9]*$')],
      persistPerPeerRate: ['', Validators.pattern('^[0-9]*$')],
      persistPerPeerCap: ['', Validators.pattern('^[0-9]*$')],
      persistTotalCap: ['', Validators.pattern('^[0-9]*$')],
      persistTtl: ['', Validators.pattern('^[0-9]*$')],
      persistSeed: ['', Validators.pattern('^[0-9]*$')],
      // Pairing.
      pairEnable: [false],
    });

    this.formSubscription = this.form.get('localhostOnly').valueChanges.subscribe(value => {
      // If "no" is selected ask for confirmation.
      if (!value) {
        this.form.get('localhostOnly').setValue(true);
        const confirmationDialog = GeneralUtils.createConfirmationDialog(this.dialog, 'apps.skychat-settings.non-localhost-confirmation');

        confirmationDialog.componentInstance.operationAccepted.subscribe(() => {
          confirmationDialog.componentInstance.closeModal();

          this.form.get('localhostOnly').setValue(false, { emitEvent: false });
        });
      }
    });

    // Get the current values saved on the visor, if returned by the API.
    if (this.data.args && this.data.args.length > 0) {
      for (let i = 0; i < this.data.args.length; i++) {
        const arg = (this.data.args[i] as string) || '';
        const next = (this.data.args[i + 1] as string) || '';
        if (arg === '-addr' && i + 1 < this.data.args.length) {
          const parts = next.split(':');
          if (parts[0] === '*') {
            this.form.get('localhostOnly').setValue(false);
          }
          this.form.get('port').setValue(parts[1]);
        }
        // Bool flags can appear as "-flag" alone or "-flag=true" or
        // "-flag true". Inspect both arg-name and value forms.
        if (arg.startsWith('-skynet')) {
          this.form.get('skynet').setValue(this.parseBool(arg, next));
        }
        if (arg.startsWith('-dmsg')) {
          this.form.get('dmsg').setValue(this.parseBool(arg, next));
        }
        if (arg.startsWith('-persist') && !arg.includes('-')) {
          // bare "-persist" — treat as true
          this.form.get('persist').setValue(true);
        }
        if (arg === '-persist') {
          this.form.get('persist').setValue(this.parseBool(arg, next));
        }
        if (arg === '-persist-max-size' && i + 1 < this.data.args.length) {
          this.form.get('persistMaxSize').setValue(next);
        }
        if (arg === '-persist-per-peer-rate' && i + 1 < this.data.args.length) {
          this.form.get('persistPerPeerRate').setValue(next);
        }
        if (arg === '-persist-per-peer-cap' && i + 1 < this.data.args.length) {
          this.form.get('persistPerPeerCap').setValue(next);
        }
        if (arg === '-persist-total-cap' && i + 1 < this.data.args.length) {
          this.form.get('persistTotalCap').setValue(next);
        }
        if (arg === '-persist-ttl' && i + 1 < this.data.args.length) {
          this.form.get('persistTtl').setValue(next);
        }
        if (arg === '-persist-seed' && i + 1 < this.data.args.length) {
          this.form.get('persistSeed').setValue(next);
        }
        if (arg === '-pair-enable') {
          this.form.get('pairEnable').setValue(this.parseBool(arg, next));
        }
      }
    }
  }

  /** Decode a bool from the various pflag forms. */
  private parseBool(arg: string, next: string): boolean {
    const eq = arg.indexOf('=');
    if (eq >= 0) { return arg.slice(eq + 1).toLowerCase() === 'true'; }
    if (next === 'true' || next === 'false') { return next === 'true'; }
    // bare "--flag" defaults to true; bare absence keeps form initial.
    return true;
  }

  ngOnDestroy() {
    if (this.formSubscription) {
      this.formSubscription.unsubscribe();
    }

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
   * Saves the settings.
   */
  saveChanges() {
    if (!this.form.valid || this.button.disabled) {
      return;
    }

    this.button.showLoading();

    // Listener address goes through the typed Address handler
    // (SetAppAddress maps it to --addr internally + port-validates).
    const data: any = {
      address: (this.form.get('localhostOnly').value ? ':' : '*:') + this.form.get('port').value,
    };

    // Everything else is generic flags routed through custom_setting.
    const cs: any = {
      skynet: this.form.get('skynet').value ? 'true' : 'false',
      dmsg: this.form.get('dmsg').value ? 'true' : 'false',
      persist: this.form.get('persist').value ? 'true' : 'false',
      'pair-enable': this.form.get('pairEnable').value ? 'true' : 'false',
    };
    // Numeric persist-* flags — empty value means "keep binary
    // default" so we omit them entirely from the customSetting map
    // rather than send empty strings (UpdateAppArgBatch with empty
    // string would write `-flag ""` rather than removing the arg).
    const num = (key: string, formKey: string) => {
      const v = this.form.get(formKey).value;
      if (v !== '' && v !== null && v !== undefined) {
        cs[key] = String(v);
      }
    };
    num('persist-max-size', 'persistMaxSize');
    num('persist-per-peer-rate', 'persistPerPeerRate');
    num('persist-per-peer-cap', 'persistPerPeerCap');
    num('persist-total-cap', 'persistTotalCap');
    num('persist-ttl', 'persistTtl');
    num('persist-seed', 'persistSeed');
    data.custom_setting = cs;

    this.operationSubscription = this.appsService.changeAppSettings(
      // The node pk is obtained from the currently openned node page.
      NodeComponent.getCurrentNodeKey(),
      this.data.name,
      data,
    ).subscribe({
      next: this.onSuccess.bind(this),
      error: this.onError.bind(this)
    });
  }

  private onSuccess() {
    NodeComponent.refreshCurrentDisplayedData();
    this.snackbarService.showDone('apps.skychat-settings.changes-made');
    this.dialogRef.close();
  }

  private onError(err: OperationError) {
    this.button.showError();
    err = processServiceError(err);

    this.snackbarService.showError(err);
  }
}
