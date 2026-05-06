import { Component, OnInit, OnDestroy, Inject } from '@angular/core';
import { UntypedFormBuilder, UntypedFormGroup } from '@angular/forms';
import { MatDialogRef, MatDialog, MatDialogConfig, MAT_DIALOG_DATA } from '@angular/material/dialog';
import { Subscription } from 'rxjs';

import { AppConfig } from 'src/app/app.config';
import { AppsService } from 'src/app/services/apps.service';
import { SnackbarService } from 'src/app/services/snackbar.service';
import { NodeComponent } from '../../node.component';
import { Application } from 'src/app/app.datatypes';

/**
 * Daemon-specific settings dialog used by the Wallet tab's "Add
 * Daemon Instance" / per-row settings buttons. The generic
 * UserAppSettingsComponent only edits args; this dialog also edits
 * the FIBER_TOML env var, which is the actual switch between vanilla
 * skycoin and a fibercoin chain.
 */
@Component({
  selector: 'app-skycoin-daemon-settings',
  templateUrl: './skycoin-daemon-settings.component.html',
  styleUrls: ['./skycoin-daemon-settings.component.scss'],
  standalone: false,
})
export class SkycoinDaemonSettingsComponent implements OnInit, OnDestroy {
  form: UntypedFormGroup;
  saving = false;
  saveError = '';

  private operationSubscription: Subscription;

  public static openDialog(dialog: MatDialog, app: Application): MatDialogRef<SkycoinDaemonSettingsComponent, any> {
    const config = new MatDialogConfig();
    config.data = app;
    config.autoFocus = false;
    config.width = AppConfig.mediumModalWidth;
    return dialog.open(SkycoinDaemonSettingsComponent, config);
  }

  constructor(
    @Inject(MAT_DIALOG_DATA) public data: Application,
    private appsService: AppsService,
    private formBuilder: UntypedFormBuilder,
    public dialogRef: MatDialogRef<SkycoinDaemonSettingsComponent>,
    private snackbarService: SnackbarService,
  ) {}

  ngOnInit() {
    // Pre-fill from the existing AppConfig: FIBER_TOML out of env,
    // --port / --data-dir / --enable-gui-api-sets out of args.
    const fiberToml = this.envValue('FIBER_TOML');
    const port = this.argValue('--port');
    const dataDir = this.argValue('--data-dir');
    const apiSets = this.argValue('--enable-gui-api-sets');

    this.form = this.formBuilder.group({
      fiberToml: [fiberToml || ''],
      port: [port || ''],
      dataDir: [dataDir || ''],
      apiSets: [apiSets || ''],
    });
  }

  ngOnDestroy(): void {
    if (this.operationSubscription) { this.operationSubscription.unsubscribe(); }
  }

  save() {
    if (this.saving) { return; }
    this.saving = true;
    this.saveError = '';

    const node = NodeComponent.currentNode.value;
    if (!node) {
      this.saveError = 'No active node';
      this.saving = false;
      return;
    }

    const appName = this.data.name;
    const customSetting: { [k: string]: string } = {};
    const port = (this.form.get('port').value || '').trim();
    const dataDir = (this.form.get('dataDir').value || '').trim();
    const apiSets = (this.form.get('apiSets').value || '').trim();
    if (port) { customSetting['--port'] = port; }
    if (dataDir) { customSetting['--data-dir'] = dataDir; }
    if (apiSets) { customSetting['--enable-gui-api-sets'] = apiSets; }

    const fiberToml = (this.form.get('fiberToml').value || '').trim();
    const env = { FIBER_TOML: fiberToml };

    // Two PUTs: args first, then env. Server accepts both in one
    // body but the typings on AppsService are split — running them
    // sequentially keeps the change list smaller.
    this.operationSubscription = this.appsService
      .changeAppSettings(node.localPk, appName, { custom_setting: customSetting })
      .subscribe({
        next: () => {
          this.appsService.setAppEnv(node.localPk, appName, env).subscribe({
            next: () => {
              this.saving = false;
              this.snackbarService.showDone('wallet.daemons.settings-saved');
              this.dialogRef.close(true);
            },
            error: (e: any) => {
              this.saving = false;
              this.saveError = (e && e.message) ? e.message : 'Failed to set FIBER_TOML';
            },
          });
        },
        error: (e: any) => {
          this.saving = false;
          this.saveError = (e && e.message) ? e.message : 'Failed to save args';
        },
      });
  }

  private envValue(key: string): string {
    const env = (this.data && (this.data as any).env) as string[] | undefined;
    if (!env) { return ''; }
    const prefix = key + '=';
    for (const e of env) {
      if (e.startsWith(prefix)) { return e.substring(prefix.length); }
    }
    return '';
  }

  private argValue(name: string): string {
    const args = this.data && this.data.args;
    if (!args) { return ''; }
    for (let i = 0; i < args.length; i++) {
      if (args[i] === name && i + 1 < args.length) {
        return args[i + 1] as string;
      }
    }
    return '';
  }
}
