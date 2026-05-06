import { Component, OnInit, OnDestroy, OnChanges, SimpleChanges, Input, Output, EventEmitter } from '@angular/core';
import { UntypedFormBuilder, UntypedFormGroup } from '@angular/forms';
import { Subscription } from 'rxjs';
import { take } from 'rxjs/operators';

import { AppsService } from 'src/app/services/apps.service';
import { SnackbarService } from 'src/app/services/snackbar.service';
import { NodeComponent } from '../../node.component';
import { Application, Node } from 'src/app/app.datatypes';

/**
 * Daemon-specific settings form. Renders inline below the matching
 * row in the Wallet tab's daemon list. Used to be a Material dialog
 * but the dialog backdrop fought the dark theme and obscured the
 * row context; inline rendering keeps the daemon's status pill +
 * Start/Stop visible alongside its settings.
 *
 * The host is responsible for showing/hiding the form via @if; this
 * component just emits `saved` / `cancelled` so the host can collapse
 * the slot.
 */
@Component({
  selector: 'app-skycoin-daemon-settings',
  templateUrl: './skycoin-daemon-settings.component.html',
  styleUrls: ['./skycoin-daemon-settings.component.scss'],
  standalone: false,
})
export class SkycoinDaemonSettingsComponent implements OnInit, OnChanges, OnDestroy {
  @Input() app!: Application;
  @Output() saved = new EventEmitter<void>();
  @Output() cancelled = new EventEmitter<void>();

  form: UntypedFormGroup;
  saving = false;
  saveError = '';

  private operationSubscription: Subscription;

  constructor(
    private appsService: AppsService,
    private formBuilder: UntypedFormBuilder,
    private snackbarService: SnackbarService,
  ) {}

  ngOnInit() {
    this.buildForm();
  }

  // The host can swap which app is bound (e.g. a different daemon's
  // expand button is clicked). Re-seed the form with the new values.
  ngOnChanges(changes: SimpleChanges) {
    if (changes['app'] && this.form) {
      this.buildForm();
    }
  }

  ngOnDestroy(): void {
    if (this.operationSubscription) { this.operationSubscription.unsubscribe(); }
  }

  private buildForm() {
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

  cancel() {
    this.cancelled.emit();
  }

  save() {
    if (this.saving) { return; }
    this.saving = true;
    this.saveError = '';
    NodeComponent.currentNode.pipe(take(1)).subscribe((node: Node) => {
      this.doSave(node);
    });
  }

  private doSave(node: Node) {
    if (!node) {
      this.saveError = 'No active node';
      this.saving = false;
      return;
    }

    const appName = this.app.name;
    const customSetting: { [k: string]: string } = {};
    const port = (this.form.get('port').value || '').trim();
    const dataDir = (this.form.get('dataDir').value || '').trim();
    const apiSets = (this.form.get('apiSets').value || '').trim();
    if (port) { customSetting['--port'] = port; }
    if (dataDir) { customSetting['--data-dir'] = dataDir; }
    if (apiSets) { customSetting['--enable-gui-api-sets'] = apiSets; }

    const fiberToml = (this.form.get('fiberToml').value || '').trim();
    const env = { FIBER_TOML: fiberToml };

    this.operationSubscription = this.appsService
      .changeAppSettings(node.localPk, appName, { custom_setting: customSetting })
      .subscribe({
        next: () => {
          this.appsService.setAppEnv(node.localPk, appName, env).subscribe({
            next: () => {
              this.saving = false;
              this.snackbarService.showDone('wallet.daemons.settings-saved');
              this.saved.emit();
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
    const env = (this.app && (this.app as any).env) as string[] | undefined;
    if (!env) { return ''; }
    const prefix = key + '=';
    for (const e of env) {
      if (e.startsWith(prefix)) { return e.substring(prefix.length); }
    }
    return '';
  }

  private argValue(name: string): string {
    const args = this.app && this.app.args;
    if (!args) { return ''; }
    for (let i = 0; i < args.length; i++) {
      if (args[i] === name && i + 1 < args.length) {
        return args[i + 1] as string;
      }
    }
    return '';
  }
}
